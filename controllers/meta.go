package controllers

import (
	"fmt"
	"strings"

	pkgerrors "github.com/pkg/errors"
	"github.com/sethvargo/go-password/password"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	metaStoreKey    = "broker_meta_store"
	metaPasswordKey = "broker_meta_password"
	// The service is provided by this operator.
	metaServiceName = "undermoon-meta"
	metaServiceHost = "undermoon-operator"
	metaServicePort = 9999
)

var errInvalidStorageName = pkgerrors.New("invalid broker storage name")

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

func createMetaSecret(cr *undermoonv1alpha1.Undermoon, password string) *corev1.Secret {
	undermoonName := cr.ObjectMeta.Name

	labels := map[string]string{
		"undermoonName":        undermoonName,
		"undermoonClusterName": cr.Spec.ClusterName,
	}

	data := make(map[string][]byte)
	data[metaPasswordKey] = []byte(password)

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      MetaSecretName(undermoonName),
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Data: data,
	}
}

// MetaSecretName defines the name for meta ConfigMap.
func MetaSecretName(undermoonName string) string {
	return fmt.Sprintf("%s-sc", undermoonName)
}

func genBrokerPassword() (string, error) {
	return password.Generate(64, 10, 10, false, false)
}

func brokerStorageName(undermoonName, namespace string) string {
	return fmt.Sprintf("%s@%s", undermoonName, namespace)
}

func extractStorageName(name string) (string, string, error) {
	segs := strings.SplitN(name, "@", 2)
	if len(segs) != 2 {
		return "", "", pkgerrors.Errorf("invalid storage name: %s", name)
	}

	undermooName := segs[0]
	namespace := segs[1]
	if len(undermooName) == 0 || len(namespace) == 0 {
		return "", "", pkgerrors.Errorf("invalid storage name: %s", name)
	}

	return undermooName, namespace, nil
}
