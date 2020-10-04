package controllers

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io/ioutil"
	"sync"

	"github.com/go-redis/redis/v8"
	pkgerrors "github.com/pkg/errors"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const chunkNodeNumber int = 4
const halfChunkNodeNumber int = 2

var errRetryReconciliation = pkgerrors.New("retry reconciliation")

func createServiceGuard(createFunc func() (*corev1.Service, error)) (*corev1.Service, error) {
	var svc *corev1.Service
	var err error
	for i := 0; i != 3; i++ {
		svc, err = createFunc()
		if err == nil {
			return svc, err
		}
		if errors.IsAlreadyExists(err) {
			continue
		}
		return nil, err
	}
	return nil, err
}

func createStatefulSetGuard(createFunc func() (*appsv1.StatefulSet, error)) (*appsv1.StatefulSet, error) {
	var ss *appsv1.StatefulSet
	var err error
	for i := 0; i != 3; i++ {
		ss, err = createFunc()
		if err == nil {
			return ss, err
		}
		if errors.IsAlreadyExists(err) {
			continue
		}
		return nil, err
	}
	return nil, err
}

func createConfigMapGuard(createFunc func() (*corev1.ConfigMap, error)) (*corev1.ConfigMap, error) {
	var cfg *corev1.ConfigMap
	var err error
	for i := 0; i != 3; i++ {
		cfg, err = createFunc()
		if err == nil {
			return cfg, err
		}
		if errors.IsAlreadyExists(err) {
			continue
		}
		return nil, err
	}
	return nil, err
}

func createSecretGuard(createFunc func() (*corev1.Secret, error)) (*corev1.Secret, error) {
	var secret *corev1.Secret
	var err error
	for i := 0; i != 3; i++ {
		secret, err = createFunc()
		if err == nil {
			return secret, err
		}
		if errors.IsAlreadyExists(err) {
			continue
		}
		return nil, err
	}
	return nil, err
}

func getEndpoints(client client.Client, serviceName, namespace string) ([]corev1.EndpointAddress, error) {
	endpoints := &corev1.Endpoints{}
	// The endpoints names are the same as serviceName
	err := client.Get(context.TODO(), types.NamespacedName{Name: serviceName, Namespace: namespace}, endpoints)

	if err != nil && errors.IsNotFound(err) {
		return []corev1.EndpointAddress{}, nil
	} else if err != nil {
		return nil, err
	}

	addresses := []corev1.EndpointAddress{}
	for _, subnet := range endpoints.Subsets {
		addresses = append(addresses, subnet.Addresses...)
	}

	return addresses, nil
}

const podIPEnvStr = "$(UM_POD_IP)"
const podNameStr = "$(UM_POD_NAME)"

func podIPEnv() corev1.EnvVar {
	return corev1.EnvVar{
		Name: "UM_POD_IP",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: "status.podIP",
			},
		},
	}
}

func podNameEnv() corev1.EnvVar {
	return corev1.EnvVar{
		Name: "UM_POD_NAME",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: "metadata.name",
			},
		},
	}
}

func getEnvValue(envs []corev1.EnvVar, name string) string {
	for _, env := range envs {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

type redisClientPool struct {
	lock    sync.Mutex
	clients map[string]*redis.Client
}

func newRedisClientPool() *redisClientPool {
	return &redisClientPool{
		lock:    sync.Mutex{},
		clients: make(map[string]*redis.Client),
	}
}

func (pool *redisClientPool) getClient(redisAddress string) *redis.Client {
	pool.lock.Lock()
	defer pool.lock.Unlock()

	if client, ok := pool.clients[redisAddress]; ok {
		return client
	}

	client := redis.NewClient(&redis.Options{
		Addr: redisAddress,
	})
	pool.clients[redisAddress] = client
	return client
}

func genAntiAffinity(labels map[string]string, namespace, topologyKey string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
				{
					Weight: 2,
					PodAffinityTerm: corev1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: labels,
						},
						Namespaces:  []string{namespace},
						TopologyKey: topologyKey,
					},
				},
			},
		},
	}
}

func genPreStopHookLifeCycle(cmd []string) *corev1.Lifecycle {
	return &corev1.Lifecycle{
		PreStop: &corev1.Handler{
			Exec: &corev1.ExecAction{Command: cmd},
		},
	}
}

func resourceRequirementsEqual(lhs, rhs corev1.ResourceRequirements) bool {
	if !resourceListEqual(lhs.Limits, rhs.Limits) {
		return false
	}

	if !resourceListEqual(lhs.Requests, rhs.Requests) {
		return false
	}

	return true
}

func resourceListEqual(lhs, rhs corev1.ResourceList) bool {
	if len(lhs) != len(rhs) {
		return false
	}

	for resourceName, lhsQuantity := range lhs {
		rhsQuantity, ok := rhs[resourceName]
		if !ok {
			return false
		}
		if !lhsQuantity.Equal(rhsQuantity) {
			return false
		}
	}

	return true
}

func compressString(origin string) (string, error) {
	var b bytes.Buffer
	w, err := gzip.NewWriterLevel(&b, gzip.BestCompression)
	if err != nil {
		return "", err
	}

	_, err = w.Write([]byte(origin))
	if err != nil {
		return "", err
	}
	// Need to close it to flush data to bytes.Buffer.
	w.Close()

	data := base64.StdEncoding.EncodeToString(b.Bytes())
	return data, nil
}

func decompressString(data string) (string, error) {
	compressed, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}

	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return "", err
	}
	// Don't need to close on error.
	defer r.Close()

	origin, err := ioutil.ReadAll(r)
	if err != nil {
		return "", err
	}

	return string(origin), nil
}
