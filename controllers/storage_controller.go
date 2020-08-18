package controllers

import (
	"context"
	"strconv"
	"strings"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type storageController struct {
	r         *UndermoonReconciler
	proxyPool *serverProxyClientPool
}

func newStorageController(r *UndermoonReconciler) *storageController {
	pool := newServerProxyClientPool()
	return &storageController{r: r, proxyPool: pool}
}

func (con *storageController) createStorage(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*appsv1.StatefulSet, *corev1.Service, error) {
	_, err := createServiceGuard(func() (*corev1.Service, error) {
		service := createStoragePublicService(cr)
		return con.getOrCreateStorageService(reqLogger, cr, service)
	})
	storageService, err := createServiceGuard(func() (*corev1.Service, error) {
		service := createStorageService(cr)
		return con.getOrCreateStorageService(reqLogger, cr, service)
	})
	if err != nil {
		reqLogger.Error(err, "failed to create storage service", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return nil, nil, err
	}

	storage, err := createStatefulSetGuard(func() (*appsv1.StatefulSet, error) {
		return con.getOrCreateStorageStatefulSet(reqLogger, cr)
	})
	if err != nil {
		reqLogger.Error(err, "failed to create storage StatefulSet", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return nil, nil, err
	}

	// Only update replica number here for scaling out.
	if int32(cr.Spec.ChunkNumber)*2 > *storage.Spec.Replicas {
		storage, err = con.updateStorageStatefulSet(reqLogger, cr, storage)
		if err != nil {
			if err != errRetryReconciliation {
				reqLogger.Error(err, "failed to update storage StatefulSet", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
			}
			return nil, nil, err
		}
	}

	return storage, storageService, nil
}

func (con *storageController) getOrCreateStorageService(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, service *corev1.Service) (*corev1.Service, error) {
	if err := controllerutil.SetControllerReference(cr, service, con.r.scheme); err != nil {
		return nil, err
	}

	found := &corev1.Service{}
	err := con.r.client.Get(context.TODO(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new storage service", "Namespace", service.Namespace, "Name", service.Name)
		err = con.r.client.Create(context.TODO(), service)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				reqLogger.Info("storage service already exists")
			} else {
				reqLogger.Error(err, "failed to create storage service")
			}
			return nil, err
		}

		reqLogger.Info("Successfully created a new storage service", "Namespace", service.Namespace, "Name", service.Name)
		return service, nil
	} else if err != nil {
		reqLogger.Error(err, "failed to get storage service")
		return nil, err
	}

	reqLogger.Info("Skip reconcile: storage service already exists", "Namespace", found.Namespace, "Name", found.Name)
	return found, nil
}

func (con *storageController) getOrCreateStorageStatefulSet(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*appsv1.StatefulSet, error) {
	storage := createStorageStatefulSet(cr)

	if err := controllerutil.SetControllerReference(cr, storage, con.r.scheme); err != nil {
		reqLogger.Error(err, "SetControllerReference failed")
		return nil, err
	}

	// Check if this storage StatefulSet already exists
	found := &appsv1.StatefulSet{}
	err := con.r.client.Get(context.TODO(), types.NamespacedName{Name: storage.Name, Namespace: storage.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new storage StatefulSet", "Namespace", storage.Namespace, "Name", storage.Name)
		err = con.r.client.Create(context.TODO(), storage)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				reqLogger.Info("storage StatefulSet already exists")
			} else {
				reqLogger.Error(err, "failed to create storage StatefulSet")
			}
			return nil, err
		}

		// StatefulSet created successfully - don't requeue
		return storage, nil
	} else if err != nil {
		reqLogger.Error(err, "failed to get storage StatefulSet", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return nil, err
	}

	// storage already exists - don't requeue
	reqLogger.Info("Skip reconcile: storage StatefulSet already exists", "Namespace", found.Namespace, "Name", found.Name)
	return found, nil
}

func (con *storageController) scaleDownStorageStatefulSet(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, storage *appsv1.StatefulSet, info *clusterInfo) (*appsv1.StatefulSet, error) {
	expectedNodeNumber := int(cr.Spec.ChunkNumber) * chunkNodeNumber
	if info.NodeNumberWithSlots > expectedNodeNumber {
		reqLogger.Info("Need to wait for slot migration to scale down storage", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return storage, errRetryReconciliation
	}

	if info.NodeNumberWithSlots < expectedNodeNumber {
		reqLogger.Info("Need to scale up", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return storage, errRetryReconciliation
	}

	storage, err := con.updateStorageStatefulSet(reqLogger, cr, storage)
	if err != nil {
		return nil, err
	}
	return storage, nil
}

func (con *storageController) updateStorageStatefulSet(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, storage *appsv1.StatefulSet) (*appsv1.StatefulSet, error) {
	replicaNum := int32(int(cr.Spec.ChunkNumber) * halfChunkNodeNumber)
	storage.Spec.Replicas = &replicaNum

	err := con.r.client.Update(context.TODO(), storage)
	if err != nil {
		if errors.IsConflict(err) {
			reqLogger.Info("Conflict on updating storage StatefulSet. Try again.")
			return nil, errRetryReconciliation
		}
		reqLogger.Error(err, "failed to update storage StatefulSet", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return nil, err
	}

	return storage, nil
}

func (con *storageController) getServiceEndpointsNum(storageService *corev1.Service) (int, error) {
	endpoints, err := getEndpoints(con.r.client, storageService.Name, storageService.Namespace)
	if err != nil {
		return 0, err
	}
	return len(endpoints), nil
}

func (con *storageController) storageReady(storageService *corev1.Service, cr *undermoonv1alpha1.Undermoon) (bool, error) {
	n, err := con.getServiceEndpointsNum(storageService)
	if err != nil {
		return false, err
	}
	serverProxyNum := int32(int(cr.Spec.ChunkNumber) * halfChunkNodeNumber)
	ready := n >= int(serverProxyNum-1)
	return ready, nil
}

func (con *storageController) storageAllReady(storageService *corev1.Service, cr *undermoonv1alpha1.Undermoon) (bool, error) {
	n, err := con.getServiceEndpointsNum(storageService)
	if err != nil {
		return false, err
	}
	serverProxyNum := int32(int(cr.Spec.ChunkNumber) * halfChunkNodeNumber)
	ready := n >= int(serverProxyNum)
	return ready, nil
}

func (con *storageController) storageAllReadyAndStable(storageService *corev1.Service, storageStatefulSet *appsv1.StatefulSet, cr *undermoonv1alpha1.Undermoon) (bool, error) {
	ready, err := con.storageAllReady(storageService, cr)
	if err != nil {
		return false, err
	}
	if !ready {
		return false, nil
	}

	statefulSetPodNum := *storageStatefulSet.Spec.Replicas
	if storageStatefulSet.Status.CurrentReplicas != statefulSetPodNum {
		return false, nil
	}
	if storageStatefulSet.Status.Replicas != statefulSetPodNum {
		return false, nil
	}
	return true, nil
}

func (con *storageController) getServerProxies(reqLogger logr.Logger, storageService *corev1.Service, cr *undermoonv1alpha1.Undermoon) ([]serverProxyMeta, error) {
	endpoints, err := getEndpoints(con.r.client, storageService.Name, storageService.Namespace)
	if err != nil {
		reqLogger.Error(err, "Failed to get endpoints of server proxies", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return nil, err
	}

	proxies := []serverProxyMeta{}
	for _, endpoint := range endpoints {
		// endpoint.Hostname is in the format of "<name>-storage-ss-<statefulset index>"
		// The prefix is the same as the result of the StorageStatefulSetName() function.
		hostname := endpoint.Hostname
		indexStr := hostname[strings.LastIndex(hostname, "-")+1:]
		index, err := strconv.ParseInt(indexStr, 10, 64)
		if err != nil {
			reqLogger.Error(err, "failed to parse storage hostname", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		}
		address := genStorageFQDNFromName(hostname, cr)
		proxy := newServerProxyMeta(address, address, cr.Spec.Port, int(index))
		proxies = append(proxies, proxy)
	}

	return proxies, nil
}

func (con *storageController) getMaxEpoch(reqLogger logr.Logger, storageService *corev1.Service, cr *undermoonv1alpha1.Undermoon) (int64, error) {
	endpoints, err := getEndpoints(con.r.client, storageService.Name, storageService.Namespace)
	if err != nil {
		reqLogger.Error(err, "Failed to get endpoints of server proxies", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return 0, err
	}

	// Filter the proxies being deleted
	sets := make(map[string]bool)
	for _, address := range genStorageStatefulSetAddrs(cr) {
		sets[address] = true
	}

	var maxEpoch int64 = 0
	for _, endpoint := range endpoints {
		address := genStorageAddressFromName(endpoint.Hostname, cr)
		if _, ok := sets[address]; !ok {
			continue
		}
		epoch, err := con.proxyPool.getEpoch(address)
		if err != nil {
			reqLogger.Error(err, "Failed to get epoch from server proxy", "proxyAddress", address, "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		}
		if epoch > maxEpoch {
			maxEpoch = epoch
		}
	}

	return maxEpoch, nil
}
