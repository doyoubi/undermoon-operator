package controllers

import (
	"fmt"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const metaStoreKey = "broker_meta_store"

func createMetaConfigMap(cr *undermoonv1alpha1.Undermoon, initData string) *corev1.ConfigMap {
	undermoonName := cr.ObjectMeta.Name

	labels := map[string]string{
		"undermoonName":        undermoonName,
		"undermoonClusterName": cr.Spec.ClusterName,
	}

	data := make(map[string]string)
	data[metaStoreKey] = initData

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      MetaConfigMapName(undermoonName),
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Data: data,
	}
}

// MetaConfigMapName defines the name for meta ConfigMap.
func MetaConfigMapName(undermoonName string) string {
	return fmt.Sprintf("%s-cfg", undermoonName)
}
