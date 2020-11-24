package controllers

import (
	"fmt"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const coordinatorPort = 6699
const coordinatorNum int32 = 3
const coordinatorContainerName = "coordinator"
const undermoonServiceTypeCoordinator = "coordinator"
const coordinatorTopologyKey = "undermoon-broker-topology-key"

func createCoordinatorService(cr *undermoonv1alpha1.Undermoon) *corev1.Service {
	undermoonName := cr.ObjectMeta.Name

	labels := map[string]string{
		"undermoonService":     undermoonServiceTypeCoordinator,
		"undermoonName":        undermoonName,
		"undermoonClusterName": cr.Spec.ClusterName,
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CoordinatorServiceName(undermoonName),
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:     "coordinator-port",
					Port:     coordinatorPort,
					Protocol: corev1.ProtocolTCP,
				},
			},
			ClusterIP: "None", // Make it a headless service
			Selector:  labels,
		},
	}
}

// CoordinatorServiceName defines the service for coordinator statefulsets.
func CoordinatorServiceName(undermoonName string) string {
	return fmt.Sprintf("%s-cd-svc", undermoonName)
}

func createCoordinatorStatefulSet(cr *undermoonv1alpha1.Undermoon) *appsv1.StatefulSet {
	labels := map[string]string{
		"undermoonService":     undermoonServiceTypeCoordinator,
		"undermoonName":        cr.ObjectMeta.Name,
		"undermoonClusterName": cr.Spec.ClusterName,
	}

	env := []corev1.EnvVar{
		{
			Name:  "RUST_LOG",
			Value: "undermoon=info,coordinator=info",
		},
		{
			Name:  "UNDERMOON_ADDRESS",
			Value: fmt.Sprintf("0.0.0.0:%d", coordinatorPort),
		},
		{
			Name:  "UNDERMOON_BROKER_ADDRESS",
			Value: "",
		},
		{
			Name: "UNDERMOON_REPORTER_ID",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name:  "UNDERMOON_THREAD_NUMBER",
			Value: "2",
		},
		{
			Name:  "UNDERMOON_PROXY_TIMEOUT",
			Value: "3",
		},
	}
	container := corev1.Container{
		Name:            coordinatorContainerName,
		Image:           cr.Spec.UndermoonImage,
		ImagePullPolicy: cr.Spec.UndermoonImagePullPolicy,
		Command:         []string{"coordinator"},
		Env:             env,
		Resources:       cr.Spec.CoordinatorResources,
	}
	podSpec := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{container},
			Affinity:   addAntiAffinity(cr.Spec.Affinity, labels, cr.ObjectMeta.Namespace, coordinatorTopologyKey),
		},
	}

	replicaNum := coordinatorNum

	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StatefulSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      CoordinatorStatefulSetName(cr.ObjectMeta.Name),
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Selector:            &metav1.LabelSelector{MatchLabels: labels},
			ServiceName:         CoordinatorServiceName(cr.ObjectMeta.Name),
			Replicas:            &replicaNum,
			Template:            podSpec,
			PodManagementPolicy: appsv1.ParallelPodManagement,
		},
	}
}

func coordinatorStatefulSetChanged(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, curr *appsv1.StatefulSet) bool {
	container := curr.Spec.Template.Spec.Containers[0]

	if cr.Spec.UndermoonImage != container.Image {
		reqLogger.Info("Coordinator image is changed.",
			"Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName,
			"OldImage", container.Image, "NewImage", cr.Spec.UndermoonImage,
		)
		return true
	}

	if cr.Spec.UndermoonImagePullPolicy != container.ImagePullPolicy {
		reqLogger.Info("Coordinator image pull policy is changed.",
			"Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName,
			"OldImagePullPolicy", container.ImagePullPolicy,
			"NewImagePullPolicy", cr.Spec.UndermoonImagePullPolicy,
		)
		return true
	}

	if !resourceRequirementsEqual(cr.Spec.CoordinatorResources, container.Resources) {
		reqLogger.Info("Coordinator resource is changed.",
			"Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName,
			"OldResource", container.Resources, "NewResource", cr.Spec.CoordinatorResources,
		)
		return true
	}

	return false
}

// CoordinatorStatefulSetName defines the statefulset for coordinator.
func CoordinatorStatefulSetName(undermoonName string) string {
	return fmt.Sprintf("%s-cd-ss", undermoonName)
}

func genCoordinatorFQDN(podName, undermoonName, namespace string) string {
	// pod-specific-string.serviceName.default.svc.cluster.local
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local", podName, CoordinatorServiceName(undermoonName), namespace)
}

func genCoordinatorAddressFromName(name string, cr *undermoonv1alpha1.Undermoon) string {
	host := genCoordinatorFQDN(name, cr.ObjectMeta.Name, cr.ObjectMeta.Namespace)
	addr := fmt.Sprintf("%s:%d", host, coordinatorPort)
	return addr
}
