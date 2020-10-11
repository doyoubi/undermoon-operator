package controllers

import (
	"fmt"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const brokerPort = 7799
const brokerNum int32 = 3
const brokerContainerName = "broker"
const undermoonServiceTypeBroker = "broker"
const brokerTopologyKey = "undermoon-broker-topology-key"

func createBrokerService(cr *undermoonv1alpha1.Undermoon) *corev1.Service {
	undermoonName := cr.ObjectMeta.Name

	labels := map[string]string{
		"undermoonService":     undermoonServiceTypeBroker,
		"undermoonName":        undermoonName,
		"undermoonClusterName": cr.Spec.ClusterName,
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      BrokerServiceName(undermoonName),
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "broker-port",
					Port:     brokerPort,
					Protocol: corev1.ProtocolTCP,
				},
			},
			ClusterIP: "None", // Make it a headless service
			Selector:  labels,
		},
	}
}

// BrokerServiceName defines the service for broker statefulsets.
func BrokerServiceName(undermoonName string) string {
	return fmt.Sprintf("%s-bk-svc", undermoonName)
}

func createBrokerStatefulSet(cr *undermoonv1alpha1.Undermoon) *appsv1.StatefulSet {
	undermoonName := cr.ObjectMeta.Name

	labels := map[string]string{
		"undermoonService":     undermoonServiceTypeBroker,
		"undermoonName":        undermoonName,
		"undermoonClusterName": cr.Spec.ClusterName,
	}

	env := []corev1.EnvVar{
		{
			Name:  "RUST_LOG",
			Value: "undermoon=info,mem_broker=info",
		},
		{
			Name:  "UNDERMOON_ADDRESS",
			Value: fmt.Sprintf("0.0.0.0:%d", brokerPort),
		},
		{
			Name:  "UNDERMOON_FAILURE_TTL",
			Value: "60",
		},
		{
			Name:  "UNDERMOON_FAILURE_QUORUM",
			Value: "2",
		},
		{
			Name:  "UNDERMOON_MIGRATION_LIMIT",
			Value: "2",
		},
		{
			Name:  "UNDERMOON_RECOVER_FROM_META_FILE",
			Value: "true",
		},
		{
			Name:  "UNDERMOON_META_FILENAME",
			Value: "metadata",
		},
		{
			Name:  "UNDERMOON_AUTO_UPDATE_META_FILE",
			Value: "true",
		},
		{
			Name:  "UNDERMOON_UPDATE_META_FILE_INTERVAL",
			Value: "10",
		},
		{
			Name:  "UNDERMOON_REPLICA_ADDRESSES",
			Value: "",
		},
		{
			Name:  "UNDERMOON_SYNC_META_INTERVAL",
			Value: "5",
		},
		{
			Name:  "UNDERMOON_ENABLE_ORDERED_PROXY",
			Value: "true",
		},
		{
			Name:  "UNDERMOON_DEBUG",
			Value: "false",
		},
		{
			Name:  "UNDERMOON_STORAGE_TYPE",
			Value: "http",
		},
		{
			Name:  "UNDERMOON_STORAGE_NAME",
			Value: brokerStorageName(undermoonName, cr.Namespace),
		},
		{
			Name: "UNDERMOON_STORAGE_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: MetaSecretName(undermoonName),
					},
					Key: metaPasswordKey,
				},
			},
		},
		{
			Name:  "UNDERMOON_HTTP_STORAGE_ADDRESS",
			Value: fmt.Sprintf("%s:%d", metaServiceHost, metaServicePort),
		},
		{
			Name:  "UNDERMOON_REFRESH_INTERVAL",
			Value: "5",
		},
	}
	container := corev1.Container{
		Name:            brokerContainerName,
		Image:           cr.Spec.UndermoonImage,
		ImagePullPolicy: cr.Spec.UndermoonImagePullPolicy,
		Command:         []string{"mem_broker"},
		Env:             env,
		Resources:       cr.Spec.BrokerResources,
	}
	podSpec := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{container},
			Affinity:   genAntiAffinity(labels, cr.ObjectMeta.Namespace, brokerTopologyKey),
		},
	}

	replicaNum := brokerNum

	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      BrokerStatefulSetName(cr.ObjectMeta.Name),
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Selector:            &metav1.LabelSelector{MatchLabels: labels},
			ServiceName:         BrokerServiceName(cr.ObjectMeta.Name),
			Replicas:            &replicaNum,
			Template:            podSpec,
			PodManagementPolicy: appsv1.ParallelPodManagement,
		},
	}
}

func brokerStatefulSetChanged(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, curr *appsv1.StatefulSet) bool {
	container := curr.Spec.Template.Spec.Containers[0]

	if cr.Spec.UndermoonImage != container.Image {
		reqLogger.Info("Broker image is changed.",
			"Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName,
			"OldImage", container.Image, "NewImage", cr.Spec.UndermoonImage,
		)
		return true
	}

	if cr.Spec.UndermoonImagePullPolicy != container.ImagePullPolicy {
		reqLogger.Info("Broker image pull policy is changed.",
			"Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName,
			"OldImagePullPolicy", container.ImagePullPolicy,
			"NewImagePullPolicy", cr.Spec.UndermoonImagePullPolicy,
		)
		return true
	}

	if !resourceRequirementsEqual(cr.Spec.BrokerResources, container.Resources) {
		reqLogger.Info("Broker resource is changed.",
			"Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName,
			"OldResource", container.Resources, "NewResource", cr.Spec.BrokerResources,
		)
		return true
	}

	return false
}

// BrokerStatefulSetName defines the statefulset for memory broker.
func BrokerStatefulSetName(undermoonName string) string {
	return fmt.Sprintf("%s-bk-ss", undermoonName)
}

func genBrokerFQDN(podName, undermoonName, namespace string) string {
	// pod-specific-string.serviceName.default.svc.cluster.local
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local", podName, BrokerServiceName(undermoonName), namespace)
}

func genBrokerAddressFromName(name string, cr *undermoonv1alpha1.Undermoon) string {
	host := genBrokerFQDN(name, cr.ObjectMeta.Name, cr.ObjectMeta.Namespace)
	addr := fmt.Sprintf("%s:%d", host, brokerPort)
	return addr
}
