package controllers

import (
	"context"
	"encoding/json"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	pkgerrors "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	errExternalStoreConflict = pkgerrors.New("ResourceVersion conflict")
)

type metaController struct {
	r      *UndermoonReconciler
	client *brokerClient
}

func newMetaController(r *UndermoonReconciler) *metaController {
	client := newBrokerClient()
	return &metaController{
		r:      r,
		client: client,
	}
}

func (con *metaController) reconcileMeta(
	reqLogger logr.Logger,
	masterBrokerAddress string,
	replicaAddresses []string,
	proxies []serverProxyMeta,
	cr *undermoonv1alpha1.Undermoon,
	storageAllReady bool,
) (*clusterInfo, error) {
	err := con.setBrokerReplicas(reqLogger, masterBrokerAddress, replicaAddresses, cr)
	if err != nil {
		return nil, err
	}

	err = con.reconcileServerProxyRegistry(reqLogger, masterBrokerAddress, proxies, cr)
	if err != nil {
		return nil, err
	}

	if !storageAllReady {
		return nil, errRetryReconciliation
	}

	err = con.createCluster(reqLogger, masterBrokerAddress, cr)
	if err != nil {
		return nil, err
	}

	info, err := con.getClusterInfo(reqLogger, masterBrokerAddress, cr)
	if err != nil {
		return nil, err
	}

	return info, nil
}

func (con *metaController) setBrokerReplicas(reqLogger logr.Logger, masterBrokerAddress string, replicaAddresses []string, cr *undermoonv1alpha1.Undermoon) error {
	err := con.client.setBrokerReplicas(masterBrokerAddress, replicaAddresses)
	if err != nil {
		if err == errRetryReconciliation {
			return err
		}
		reqLogger.Error(err, "failed to set broker replicas", "masterBrokerAddress", masterBrokerAddress, "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return err
	}

	for _, replicaAddress := range replicaAddresses {
		err := con.client.setBrokerReplicas(replicaAddress, []string{})
		if err != nil {
			if err == errRetryReconciliation {
				return err
			}
			reqLogger.Error(err, "failed to set broker replicas", "replicaBrokerAddress", replicaAddress, "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
			return err
		}
	}
	return nil
}

func (con *metaController) changeMeta(reqLogger logr.Logger, masterBrokerAddress string, cr *undermoonv1alpha1.Undermoon, info *clusterInfo) error {
	if info.IsMigrating {
		return errRetryReconciliation
	}

	err := con.changeNodeNumber(reqLogger, masterBrokerAddress, cr)
	if err != nil {
		if err == errMigrationRunning {
			return errRetryReconciliation
		}
		return err
	}

	return nil
}

func (con *metaController) reconcileServerProxyRegistry(reqLogger logr.Logger, masterBrokerAddress string, proxies []serverProxyMeta, cr *undermoonv1alpha1.Undermoon) error {
	err := con.registerServerProxies(reqLogger, masterBrokerAddress, proxies, cr)
	if err != nil {
		return err
	}

	err = con.deregisterServerProxies(reqLogger, masterBrokerAddress, proxies, cr)
	if err != nil {
		return err
	}

	return nil
}

func (con *metaController) registerServerProxies(reqLogger logr.Logger, masterBrokerAddress string, proxies []serverProxyMeta, cr *undermoonv1alpha1.Undermoon) error {
	for _, proxy := range proxies {
		err := con.client.registerServerProxy(masterBrokerAddress, proxy)
		if err != nil {
			if err == errRetryReconciliation {
				return err
			}
			reqLogger.Error(err, "failed to register server proxy", "proxy", proxy, "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		}
	}
	return nil
}

func (con *metaController) deregisterServerProxies(reqLogger logr.Logger, masterBrokerAddress string, proxies []serverProxyMeta, cr *undermoonv1alpha1.Undermoon) error {
	existingProxies, err := con.client.getServerProxies(masterBrokerAddress)
	if err != nil {
		reqLogger.Error(err, "failed to get server proxy addresses",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}

	keepSet := make(map[string]bool)
	// Need to include the failed but still in use proxies.
	for _, proxyAddress := range genStorageStatefulSetAddrs(cr) {
		keepSet[proxyAddress] = true
	}
	// Need to include the proxies waiting to scale down.
	for _, proxy := range proxies {
		keepSet[proxy.ProxyAddress] = true
	}

	deleteList := []string{}
	for _, existingAddress := range existingProxies {
		if _, ok := keepSet[existingAddress]; !ok {
			deleteList = append(deleteList, existingAddress)
		}
	}

	for _, deleteAddress := range deleteList {
		err := con.client.deregisterServerProxy(masterBrokerAddress, deleteAddress)
		if err != nil {
			if err == errRetryReconciliation {
				return err
			}
			reqLogger.Error(err, "failed to deregister server proxy",
				"proxyAddress", deleteAddress,
				"Name", cr.ObjectMeta.Name,
				"ClusterName", cr.Spec.ClusterName)
		}
	}

	return nil
}

func (con *metaController) createCluster(reqLogger logr.Logger, masterBrokerAddress string, cr *undermoonv1alpha1.Undermoon) error {
	exists, err := con.client.clusterExists(masterBrokerAddress, cr.Spec.ClusterName)
	if err != nil {
		reqLogger.Error(err, "failed to check whether cluster exists",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}

	if exists {
		return nil
	}

	err = con.client.createCluster(masterBrokerAddress, cr.Spec.ClusterName, int(cr.Spec.ChunkNumber))
	if err != nil {
		if err == errRetryReconciliation {
			return err
		}
		reqLogger.Error(err, "failed to create cluster",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}
	return nil
}

func (con *metaController) changeNodeNumber(reqLogger logr.Logger, masterBrokerAddress string, cr *undermoonv1alpha1.Undermoon) error {
	chunkNumber := int(cr.Spec.ChunkNumber)
	clusterName := cr.Spec.ClusterName

	err := con.client.scaleNodes(masterBrokerAddress, clusterName, chunkNumber)
	retry := err == errFreeNodeFound
	if err != nil && err != errFreeNodeFound {
		if err == errMigrationRunning {
			return errRetryReconciliation
		}
		if err == errRetryReconciliation {
			return err
		}
		reqLogger.Error(err, "failed to scale nodes",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}

	err = con.client.removeFreeNodes(masterBrokerAddress, clusterName)
	if err != nil {
		if err == errMigrationRunning {
			return errRetryReconciliation
		}
		if err == errRetryReconciliation {
			return err
		}
		reqLogger.Error(err, "failed to remove free nodes",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}

	if retry {
		return errRetryReconciliation
	}

	return nil
}

func (con *metaController) getClusterInfo(reqLogger logr.Logger, masterBrokerAddress string, cr *undermoonv1alpha1.Undermoon) (*clusterInfo, error) {
	info, err := con.client.getClusterInfo(masterBrokerAddress, cr.Spec.ClusterName)
	if err != nil {
		reqLogger.Error(err, "failed to get cluster info",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return nil, err
	}
	return info, nil
}

func (con *metaController) createMeta(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) error {
	_, err := createConfigMapGuard(func() (*corev1.ConfigMap, error) {
		return con.getOrCreateConfigMap(reqLogger, cr)
	})
	if err != nil {
		reqLogger.Error(err, "failed to create meta configmap")
		return err
	}

	_, err = createSecretGuard(func() (*corev1.Secret, error) {
		return con.getOrCreateMetaSecret(reqLogger, cr)
	})
	if err != nil {
		reqLogger.Error(err, "failed to generate password")
		return err
	}

	return nil
}

func (con *metaController) getOrCreateConfigMap(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*corev1.ConfigMap, error) {
	initData, err := compressString("{}")
	if err != nil {
		reqLogger.Error(err, "failed to compress init data")
		return nil, err
	}

	configmap := createMetaConfigMap(cr, initData)

	if err := controllerutil.SetControllerReference(cr, configmap, con.r.scheme); err != nil {
		reqLogger.Error(err, "SetControllerReference failed")
		return nil, err
	}

	// Check if this meta ConfigMap already exists
	found := &corev1.ConfigMap{}
	err = con.r.client.Get(context.TODO(), types.NamespacedName{Name: configmap.Name, Namespace: configmap.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new meta configmap", "Namespace", configmap.Namespace, "Name", configmap.Name)
		err = con.r.client.Create(context.TODO(), configmap)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				reqLogger.Info("meta configmap already exists")
			} else {
				reqLogger.Error(err, "failed to create meta configmap")
			}
			return nil, err
		}
		// ConfigMap created successfully - don't requeue
		return configmap, nil
	} else if err != nil {
		reqLogger.Error(err, "failed to get meta configmap")
		return nil, err
	}

	// configmap already exists - don't requeue
	reqLogger.Info("Skip reconcile: meta configmap already exists", "Namespace", found.Namespace, "Name", found.Name)
	return found, nil
}

func (con *metaController) getExternalStore(reqLogger logr.Logger, undermoonName, namespace string) (*externalStore, error) {
	configmap, err := con.getConfigMap(reqLogger, undermoonName, namespace)
	if err != nil {
		return nil, err
	}

	data, ok := configmap.Data[metaStoreKey]
	if !ok {
		err = pkgerrors.New("configmap is deleted")
		reqLogger.Error(err, "configmap is deleted")
		return nil, err
	}

	decompressData, err := decompressString(data)
	if err != nil {
		reqLogger.Error(err, "failed to decompress data")
		return nil, err
	}

	store := make(map[string]interface{})
	err = json.Unmarshal([]byte(decompressData), &store)
	if err != nil {
		reqLogger.Error(err, "failed to decode json")
		return nil, err
	}

	return &externalStore{
		Version: configmap.ObjectMeta.ResourceVersion,
		Store:   store,
	}, nil
}

func (con *metaController) initOrUpdateExternalStore(reqLogger logr.Logger, undermoonName, namespace string, store *externalStore) error {
	// null values will become empty strings.
	if len(store.Version) == 0 {
		return con.initExternalStore(reqLogger, undermoonName, namespace, store.Store)
	}
	return con.updateExternalStore(reqLogger, undermoonName, namespace, store)
}

func (con *metaController) initExternalStore(reqLogger logr.Logger, undermoonName, namespace string, store map[string]interface{}) error {
	currStore, err := con.getExternalStore(reqLogger, undermoonName, namespace)
	if err != nil {
		return err
	}

	if len(currStore.Store) != 0 {
		reqLogger.Info("external storage has already initialized")
		return nil
	}

	currStore.Store = store
	return con.updateExternalStore(reqLogger, undermoonName, namespace, currStore)
}

func (con *metaController) updateExternalStore(reqLogger logr.Logger, undermoonName, namespace string, store *externalStore) error {
	configmap, err := con.getConfigMap(reqLogger, undermoonName, namespace)
	if err != nil {
		return err
	}

	if configmap.ObjectMeta.ResourceVersion != store.Version {
		reqLogger.Info("version pre-check conflict",
			"requestedVersion", store.Version,
			"currentVersion", configmap.ObjectMeta.ResourceVersion,
		)
		return errExternalStoreConflict
	}

	data, err := json.Marshal(store.Store)
	if err != nil {
		reqLogger.Error(err, "failed to encode store")
		return nil
	}

	compressedData, err := compressString(string(data))
	if err != nil {
		reqLogger.Error(err, "failed to compress store")
		return err
	}

	configmap.Data[metaStoreKey] = compressedData

	err = con.r.client.Update(context.Background(), configmap)
	if err != nil && errors.IsConflict(err) {
		reqLogger.Info("failed to update store in configmap: conflict",
			"requestedVersion", store.Version)
		return errExternalStoreConflict
	} else if err != nil {
		reqLogger.Error(err, "failed to update store in configmap")
		return err
	}

	return nil
}

func (con *metaController) getConfigMap(reqLogger logr.Logger, undermoonName, namespace string) (*corev1.ConfigMap, error) {
	configmapName := MetaConfigMapName(undermoonName)

	found := &corev1.ConfigMap{}
	err := con.r.client.Get(context.TODO(), types.NamespacedName{Name: configmapName, Namespace: namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Error(err, "failed to get configmap: not found")
		return nil, err
	} else if err != nil {
		reqLogger.Error(err, "failed to get configmap")
		return nil, err
	}

	return found, nil
}

func (con *metaController) getOrCreateMetaSecret(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*corev1.Secret, error) {
	password, err := genBrokerPassword()
	if err != nil {
		reqLogger.Error(err, "failed to generate broker password")
		return nil, err
	}

	secret := createMetaSecret(cr, password)

	if err := controllerutil.SetControllerReference(cr, secret, con.r.scheme); err != nil {
		reqLogger.Error(err, "SetControllerReference failed")
		return nil, err
	}

	// Check if this meta Secret already exists
	found := &corev1.Secret{}
	err = con.r.client.Get(context.TODO(), types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new meta secret", "Name", secret.Name)
		err = con.r.client.Create(context.TODO(), secret)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				reqLogger.Info("meta secret already exists")
			} else {
				reqLogger.Error(err, "failed to create meta secret")
			}
			return nil, err
		}
		// Secret created successfully - don't requeue
		return secret, nil
	} else if err != nil {
		reqLogger.Error(err, "failed to get meta secret")
		return nil, err
	}

	// secret already exists - don't requeue
	reqLogger.Info("Skip reconcile: meta secret already exists", "Name", found.Name)
	return found, nil
}

func (con *metaController) checkMetaSecret(reqLogger logr.Logger, undermoonName, namespace, password string) (bool, error) {
	secretName := MetaSecretName(undermoonName)

	found := &corev1.Secret{}
	err := con.r.client.Get(context.TODO(), types.NamespacedName{Name: secretName, Namespace: namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		reqLogger.Error(err, "failed to get meta secret")
		return false, err
	}

	secretPassword, ok := found.Data[metaPasswordKey]
	if !ok {
		err = pkgerrors.New("meta secret is deleted")
		reqLogger.Error(err, "meta secret is deleted")
		return false, err
	}

	return string(secretPassword) == password, nil
}
