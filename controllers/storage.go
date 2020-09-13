package controllers

import (
	"fmt"
	"strconv"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
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
const redisReplicationOffsetThreshold uint32 = 10000

// This service is only used internally for getting the created server proxies
// which have not received UMCTL SETCLUSTER.
func createStorageService(cr *undermoonv1alpha1.Undermoon) *corev1.Service {
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
func createStoragePublicService(cr *undermoonv1alpha1.Undermoon) *corev1.Service {
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

func createStorageStatefulSet(cr *undermoonv1alpha1.Undermoon) *appsv1.StatefulSet {
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
		{
			Name:  "UNDERMOON_THREAD_NUMBER",
			Value: strconv.FormatUint(uint64(cr.Spec.ProxyThreads), 10),
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
		Env:            env,
		Resources:      cr.Spec.ProxyResources,
		Lifecycle:      genPreStopHookLifeCycle([]string{"sleep", "30"}),
		ReadinessProbe: genServerProxyReadinessProbe(cr.Spec.Port),
	}
	redisContainer1 := genRedisContainer(1, cr.Spec.RedisImage, cr.Spec.MaxMemory, redisPort1, cr)
	redisContainer2 := genRedisContainer(2, cr.Spec.RedisImage, cr.Spec.MaxMemory, redisPort2, cr)

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

func genRedisContainer(index uint32, redisImage string, maxMemory, port uint32, cr *undermoonv1alpha1.Undermoon) corev1.Container {
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
			// Set an invalid replica config at the first time
			// to mark whether a redis has its role set by server-proxy.
			// This will be used in the readinessProbe to support rolling update.
			"--slaveof",
			"localhost",
			"0", // Use zero port here.
		},
		Env:            []corev1.EnvVar{podIPEnv()},
		Resources:      cr.Spec.RedisResources,
		Lifecycle:      genPreStopHookLifeCycle([]string{"sleep", "30"}),
		ReadinessProbe: genRedisReadinessProbe(port, redisReplicationOffsetThreshold),
	}
}

// This is copied from `./scripts/redis-readiness.sh`.
// See the comments there for details.
var redisReadinessScript string = `
set +e;
TAG_FILE="${0}";
PORT="${1}";
OFFSET_THRESHOLD="${2}";

tag_file_dir=$(dirname "${TAG_FILE}");
mkdir -p "${tag_file_dir}";
if test -f "${TAG_FILE}"; then
    echo "${TAG_FILE} exists";
    exit 0;
fi;

repl_info=$(redis-cli -h localhost -p "${PORT}" INFO REPLICATION);
role=$(echo "${repl_info}" | grep 'role:' | cut -d':' -f2 | tr -d '\r' );
echo "role: ${role}";

if [ "${role}" = 'master' ]; then
    echo "role: ${role}. Create tag file ${TAG_FILE}";
    touch "${TAG_FILE}";
    exit 0;
fi;

slave_repl_offset=$(echo "${repl_info}" | grep 'master_repl_offset:' | cut -d':' -f2 | tr -d '\r');
if [ "${slave_repl_offset}" -eq 0 ]; then
    echo "Zero slave_repl_offset. The replica still cannot connect to its master.";
    exit 1;
fi;

master_host=$(echo "${repl_info}" | grep 'master_host:' | cut -d':' -f2 | tr -d '\r');
master_port=$(echo "${repl_info}" | grep 'master_port:' | cut -d':' -f2 | tr -d '\r');
echo "master: ${master_host} ${master_port}";

if [ "${master_port}" -eq 0 ]; then
    echo "Zero master port. The role is not set yet.";
    exit 1;
fi;

master_repl_info=$(redis-cli -h "${master_host}" -p "${master_port}" INFO REPLICATION);
master_repl_offset=$(echo "${master_repl_info}" | grep 'master_repl_offset:' | cut -d':' -f2 | tr -d '\r');
echo "master_repl_offset: ${master_repl_offset} slave_repl_offset: ${slave_repl_offset}";
offset=$((master_repl_offset - slave_repl_offset));
echo "offset: ${offset}";

if [ "${master_repl_offset}" -gt 0 ] && [ "${offset}" -ge 0 ] && [ "${offset}" -lt "${OFFSET_THRESHOLD}" ]; then
    echo "Replication is done. Create tag file ${TAG_FILE}";
    touch "${TAG_FILE}";
    exit 0;
fi;

echo "replica pending on replication";
exit 1;
`

func genRedisReadinessProbe(port uint32, offsetThreshold uint32) *corev1.Probe {
	// When rolling update, we need to wait for the second part in the chunk
	// to synchronize all the data from the masters in the first part
	// before rebooting the first part.
	// Thus, during the synchronization we need to make this second part "not ready"
	// and kubernetes will wait for it to become ready to move to reboot the first part.
	//
	// Note that we can't put this part to the pre-stop hook
	// because no matter whether the pre-stop script finish or not,
	// the pod of first part will be tagged TERMINATING and is not available to users.
	cmd := []string{
		"sh",
		"-c",
		redisReadinessScript,
		"/redis-state/redis-ready",
		fmt.Sprintf("%d", port),
		fmt.Sprintf("%d", offsetThreshold),
	}
	return &corev1.Probe{
		Handler: corev1.Handler{
			Exec: &corev1.ExecAction{Command: cmd},
		},
		TimeoutSeconds:   3,
		PeriodSeconds:    5,
		SuccessThreshold: 1,
		FailureThreshold: 1,
	}
}

func genServerProxyReadinessProbe(serverProxyPort uint32) *corev1.Probe {
	checkCmd := []string{
		"bash",
		"-c",
		// Checks whether the server proxy has received UMCTL SETCLUSTER.
		// Send UMCTL READY to server proxy and
		// see whether it returns `:1\r\n`.
		fmt.Sprintf(
			"[ \"$(exec 5<>/dev/tcp/localhost/%d; printf '*2\r\n$5\r\nUMCTL\r\n$5\r\nREADY\r\n' >&5; head -c 2 <&5)\" == ':1' ]",
			serverProxyPort,
		),
	}
	return &corev1.Probe{
		Handler: corev1.Handler{
			Exec: &corev1.ExecAction{
				Command: checkCmd,
			},
		},
		PeriodSeconds:    1,
		SuccessThreshold: 1,
		FailureThreshold: 1,
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

func genStorageFQDNFromName(name string, cr *undermoonv1alpha1.Undermoon) string {
	host := genStorageFQDN(name, cr.ObjectMeta.Name, cr.ObjectMeta.Namespace)
	return host
}

func genStorageAddressFromName(name string, cr *undermoonv1alpha1.Undermoon) string {
	host := genStorageFQDNFromName(name, cr)
	addr := fmt.Sprintf("%s:%d", host, cr.Spec.Port)
	return addr
}

func genStorageStatefulSetAddrs(cr *undermoonv1alpha1.Undermoon) []string {
	addrs := []string{}
	replicaNum := int(cr.Spec.ChunkNumber) * halfChunkNodeNumber
	for _, name := range genStorageNames(cr.ObjectMeta.Name, replicaNum) {
		addr := genStorageAddressFromName(name, cr)
		addrs = append(addrs, addr)
	}
	return addrs
}
