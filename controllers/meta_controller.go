package controllers

import (
	cachev1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	"github.com/go-logr/logr"
)

type metaController struct {
	client *brokerClient
}

func newMetaController() *metaController {
	client := newBrokerClient()
	return &metaController{client: client}
}

func (con *metaController) reconcileMeta(reqLogger logr.Logger, masterBrokerAddress string, replicaAddresses []string, proxies []serverProxyMeta, cr *cachev1alpha1.Undermoon, storageAllReady bool) (*clusterInfo, error) {
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

func (con *metaController) setBrokerReplicas(reqLogger logr.Logger, masterBrokerAddress string, replicaAddresses []string, cr *cachev1alpha1.Undermoon) error {
	err := con.client.setBrokerReplicas(masterBrokerAddress, replicaAddresses)
	if err != nil {
		reqLogger.Error(err, "failed to set broker replicas", "masterBrokerAddress", masterBrokerAddress, "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return err
	}

	for _, replicaAddress := range replicaAddresses {
		err := con.client.setBrokerReplicas(replicaAddress, []string{})
		if err != nil {
			reqLogger.Error(err, "failed to set broker replicas", "replicaBrokerAddress", replicaAddress, "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
			return err
		}
	}
	return nil
}

func (con *metaController) changeMeta(reqLogger logr.Logger, masterBrokerAddress string, cr *cachev1alpha1.Undermoon, info *clusterInfo) error {
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

func (con *metaController) reconcileServerProxyRegistry(reqLogger logr.Logger, masterBrokerAddress string, proxies []serverProxyMeta, cr *cachev1alpha1.Undermoon) error {
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

func (con *metaController) registerServerProxies(reqLogger logr.Logger, masterBrokerAddress string, proxies []serverProxyMeta, cr *cachev1alpha1.Undermoon) error {
	for _, proxy := range proxies {
		err := con.client.registerServerProxy(masterBrokerAddress, proxy)
		if err != nil {
			reqLogger.Error(err, "failed to register server proxy", "proxy", proxy, "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		}
	}
	return nil
}

func (con *metaController) deregisterServerProxies(reqLogger logr.Logger, masterBrokerAddress string, proxies []serverProxyMeta, cr *cachev1alpha1.Undermoon) error {
	existingProxies, err := con.client.getServerProxies(masterBrokerAddress)
	if err != nil {
		reqLogger.Error(err, "failed to get server proxy addresses",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}

	keepSet := make(map[string]bool, 0)
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
			reqLogger.Error(err, "failed to deregister server proxy",
				"proxyAddress", deleteAddress,
				"Name", cr.ObjectMeta.Name,
				"ClusterName", cr.Spec.ClusterName)
		}
	}

	return nil
}

func (con *metaController) createCluster(reqLogger logr.Logger, masterBrokerAddress string, cr *cachev1alpha1.Undermoon) error {
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
		reqLogger.Error(err, "failed to create cluster",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}
	return nil
}

func (con *metaController) changeNodeNumber(reqLogger logr.Logger, masterBrokerAddress string, cr *cachev1alpha1.Undermoon) error {
	chunkNumber := int(cr.Spec.ChunkNumber)
	clusterName := cr.Spec.ClusterName

	err := con.client.scaleNodes(masterBrokerAddress, clusterName, chunkNumber)
	retry := err == errFreeNodeFound
	if err != nil && err != errFreeNodeFound {
		if err == errMigrationRunning {
			return errRetryReconciliation
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

func (con *metaController) getClusterInfo(reqLogger logr.Logger, masterBrokerAddress string, cr *cachev1alpha1.Undermoon) (*clusterInfo, error) {
	info, err := con.client.getClusterInfo(masterBrokerAddress, cr.Spec.ClusterName)
	if err != nil {
		reqLogger.Error(err, "failed to get cluster info",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return nil, err
	}
	return info, nil
}

func (con *metaController) fixBrokerEpoch(reqLogger logr.Logger, masterBrokerAddress string, maxEpochFromServerProxy int64, cr *cachev1alpha1.Undermoon) error {
	epoch, err := con.client.getEpoch(masterBrokerAddress)
	if err != nil {
		reqLogger.Error(err, "failed to get global epoch from broker",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}

	if epoch >= maxEpochFromServerProxy {
		return nil
	}

	err = con.client.fixEpoch(masterBrokerAddress)
	if err != nil {
		reqLogger.Error(err, "failed to fix broker global epoch",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}

	return nil
}
