package controllers

import (
	"fmt"
	"strconv"

	cachev1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DefaultServerProxyPort is the port for clients to connect to.
const DefaultServerProxyPort = 5299
const redisPort1 = 7001
const redisPort2 = 7002
const serverProxyContainerName = "server-proxy"
const redisContainerName = "redis"
const undermoonServiceTypeStorage = "storage"
const storageTopologyKey = "undermoon-storage-topology-key"

// This service is only used internally for getting the created server proxies
// which have not received UMCTL SETCLUSTER.
func createStorageService(cr *cachev1alpha1.Undermoon) *corev1.Service {
	undermoonName := cr.ObjectMeta.Name

	labels := map[string]string{
		"undermoonService":     undermoonServiceTypeStorage,
		"undermoonName":        undermoonName,
		"undermoonClusterName": cr.Spec.ClusterName,
	}

	// This service is only used to query the hosts and ips of the server proxies.
	// It will not be used directly.
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      StorageServiceName(undermoonName),
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "server-proxy-port",
					Port:     int32(cr.Spec.Port),
					Protocol: corev1.ProtocolTCP,
				},
			},
			ClusterIP: "None", // Make it a headless service
			Selector:  labels,
			// We need to use this service to discover not ready server proxies
			// and register them in the broker.
			PublishNotReadyAddresses: true,
		},
	}
}

// This is the service exposed to the users.
// It only exposes those server proxies which have received UMCTL SETCLUSTER
// and had metadata set up.
func createStoragePublicService(cr *cachev1alpha1.Undermoon) *corev1.Service {
	undermoonName := cr.ObjectMeta.Name

	labels := map[string]string{
		"undermoonService":     undermoonServiceTypeStorage,
		"undermoonName":        undermoonName,
		"undermoonClusterName": cr.Spec.ClusterName,
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      StoragePublicServiceName(undermoonName),
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "server-proxy-public-port",
					Port:     int32(cr.Spec.Port),
					Protocol: corev1.ProtocolTCP,
				},
			},
			Selector: labels,
		},
	}
}

// StorageServiceName defines the service for storage StatefulSet.
func StorageServiceName(undermoonName string) string {
	return fmt.Sprintf("%s-stg-svc", undermoonName)
}

// StoragePublicServiceName defines the service for storage StatefulSet.
func StoragePublicServiceName(undermoonName string) string {
	return undermoonName
}

func createStorageStatefulSet(cr *cachev1alpha1.Undermoon) *appsv1.StatefulSet {
	labels := map[string]string{
		"undermoonService":     undermoonServiceTypeStorage,
		"undermoonName":        cr.ObjectMeta.Name,
		"undermoonClusterName": cr.Spec.ClusterName,
	}

	// Use the first proxy address instead of the service address
	// for the default redirection address when scaling down
	// because using service address can result in too many
	// redirections if the majority pods are removed
	// at the same time.
	firstProxyAddress := genStorageAddressFromName(storageStatefulSetPodName(cr.ObjectMeta.Name, 0), cr)

	env := []corev1.EnvVar{
		podNameEnv(),
		{
			Name:  "RUST_LOG",
			Value: "undermoon=info,server_proxy=info",
		},
		{
			Name:  "UNDERMOON_ADDRESS",
			Value: fmt.Sprintf("0.0.0.0:%d", cr.Spec.Port),
		},
		// UNDERMOON_ANNOUNCE_ADDRESS is set in the command
		{
			Name:  "UNDERMOON_AUTO_SELECT_CLUSTER",
			Value: "true",
		},
		{
			Name:  "UNDERMOON_SLOWLOG_LEN",
			Value: "1024",
		},
		{
			Name:  "UNDERMOON_SLOWLOG_LOG_SLOWER_THAN",
			Value: "10000",
		},
		{
			Name:  "UNDERMOON_SLOWLOG_SAMPLE_RATE",
			Value: "1000",
		},
		{
			Name:  "UNDERMOON_SESSION_CHANNEL_SIZE",
			Value: "4096",
		},
		{
			Name:  "UNDERMOON_BACKEND_CHANNEL_SIZE",
			Value: "4096",
		},
		{
			Name:  "UNDERMOON_BACKEND_BATCH_MIN_TIME",
			Value: "20000",
		},
		{
			Name:  "UNDERMOON_BACKEND_BATCH_MAX_TIME",
			Value: "400000",
		},
		{
			Name:  "UNDERMOON_SESSION_BATCH_MIN_TIME",
			Value: "20000",
		},
		{
			Name:  "UNDERMOON_SESSION_BATCH_MAX_TIME",
			Value: "400000",
		},
		{
			Name:  "UNDERMOON_ACTIVE_REDIRECTION",
			Value: strconv.FormatBool(cr.Spec.ActiveRedirection),
		},
		{
			Name:  "UNDERMOON_DEFAULT_REDIRECTION_ADDRESS",
			Value: firstProxyAddress,
		},
	}

	fqdn := genStorageFQDNFromName(podNameStr, cr)
	serverProxyContainer := corev1.Container{
		Name:            serverProxyContainerName,
		Image:           cr.Spec.UndermoonImage,
		ImagePullPolicy: cr.Spec.UndermoonImagePullPolicy,
		Command: []string{
			"sh",
			"-c",
			fmt.Sprintf("UNDERMOON_ANNOUNCE_ADDRESS=\"%s:%d\" server_proxy", fqdn, cr.Spec.Port),
		},
		Env:       env,
		Resources: cr.Spec.ProxyResources,
		Lifecycle: genPreStopHookLifeCycle([]string{"sleep", "10"}),
	}
	redisContainer1 := genRedisContainer(1, cr.Spec.RedisImage, cr.Spec.MaxMemory, redisPort1, cr)
	redisContainer2 := genRedisContainer(2, cr.Spec.RedisImage, cr.Spec.MaxMemory, redisPort2, cr)

	checkCmd := []string{
		"bash",
		"-c",
		// Checks whether the server proxy has received UMCTL SETCLUSTER.
		// Send UMCTL READY to server proxy and
		// see whether it returns `:1\r\n`.
		fmt.Sprintf(
			"[ \"$(exec 5<>/dev/tcp/localhost/%d; printf '*2\r\n$5\r\nUMCTL\r\n$5\r\nREADY\r\n' >&5; head -c 2 <&5)\" == ':1' ]",
			cr.Spec.Port,
		),
	}
	serverProxyContainer.ReadinessProbe = &corev1.Probe{
		Handler: corev1.Handler{
			Exec: &corev1.ExecAction{
				Command: checkCmd,
			},
		},
		PeriodSeconds:    1,
		SuccessThreshold: 1,
		FailureThreshold: 1,
	}

	podSpec := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				serverProxyContainer,
				redisContainer1,
				redisContainer2,
			},
			Affinity: genAntiAffinity(labels, cr.ObjectMeta.Namespace, storageTopologyKey),
		},
	}

	replicaNum := int32(int(cr.Spec.ChunkNumber) * halfChunkNodeNumber)

	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      StorageStatefulSetName(cr.ObjectMeta.Name),
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Selector:            &metav1.LabelSelector{MatchLabels: labels},
			ServiceName:         StorageServiceName(cr.ObjectMeta.Name),
			Replicas:            &replicaNum,
			Template:            podSpec,
			PodManagementPolicy: appsv1.ParallelPodManagement,
		},
	}
}

func genRedisContainer(index uint32, redisImage string, maxMemory, port uint32, cr *cachev1alpha1.Undermoon) corev1.Container {
	portStr := fmt.Sprintf("%d", port)
	return corev1.Container{
		Name:            fmt.Sprintf("%s-%d", redisContainerName, index),
		Image:           redisImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"redis-server"},
		Args: []string{
			"--maxmemory",
			fmt.Sprintf("%dMB", maxMemory),
			"--port",
			portStr,
			"--slave-announce-port",
			portStr,
			"--slave-announce-ip",
			podIPEnvStr,
			"--maxmemory-policy",
			"allkeys-lru",
		},
		Env:       []corev1.EnvVar{podIPEnv()},
		Resources: cr.Spec.RedisResources,
		Lifecycle: genPreStopHookLifeCycle([]string{"sleep", "10"}),
	}
}

// StorageStatefulSetName defines the StatefulSet for server proxy.
func StorageStatefulSetName(undermoonName string) string {
	return fmt.Sprintf("%s-stg-ss", undermoonName)
}

func storageStatefulSetPodName(undermoonName string, index int) string {
	return fmt.Sprintf("%s-%d", StorageStatefulSetName(undermoonName), index)
}

func genStorageNames(undermoonName string, replicas int) []string {
	names := []string{}
	for i := 0; i != replicas; i++ {
		name := storageStatefulSetPodName(undermoonName, i)
		names = append(names, name)
	}
	return names
}

func genStorageFQDN(podName, undermoonName, namespace string) string {
	// pod-specific-string.serviceName.default.svc.cluster.local
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local", podName, StorageServiceName(undermoonName), namespace)
}

func genStorageFQDNFromName(name string, cr *cachev1alpha1.Undermoon) string {
	host := genStorageFQDN(name, cr.ObjectMeta.Name, cr.ObjectMeta.Namespace)
	return host
}

func genStorageAddressFromName(name string, cr *cachev1alpha1.Undermoon) string {
	host := genStorageFQDNFromName(name, cr)
	addr := fmt.Sprintf("%s:%d", host, cr.Spec.Port)
	return addr
}

func genStorageStatefulSetAddrs(cr *cachev1alpha1.Undermoon) []string {
	addrs := []string{}
	replicaNum := int(cr.Spec.ChunkNumber) * halfChunkNodeNumber
	for _, name := range genStorageNames(cr.ObjectMeta.Name, replicaNum) {
		addr := genStorageAddressFromName(name, cr)
		addrs = append(addrs, addr)
	}
	return addrs
}
