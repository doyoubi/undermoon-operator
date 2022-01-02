package controllers

import (
	"context"
	"strconv"
	"strings"
	"time"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	pkgerrors "github.com/pkg/errors"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	scaleStateStable        = "Stable"
	scaleStateScaleDownWait = "ScaleDownWait"

	scaleDownWaitTime = time.Second * 30
)

type storageController struct {
	r *UndermoonReconciler
}

func newStorageController(r *UndermoonReconciler) *storageController {
	return &storageController{r: r}
}

func (con *storageController) createStorage(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*appsv1.StatefulSet, *corev1.Service, error) {
	_, err := createServiceGuard(func() (*corev1.Service, error) {
		service := createStoragePublicService(cr)
		return con.getOrCreateStorageService(reqLogger, cr, service)
	})
	if err != nil {
		reqLogger.Error(err, "failed to create storage public service")
		return nil, nil, err
	}

	storageService, err := createServiceGuard(func() (*corev1.Service, error) {
		service := createStorageService(cr)
		return con.getOrCreateStorageService(reqLogger, cr, service)
	})
	if err != nil {
		reqLogger.Error(err, "failed to create storage service")
		return nil, nil, err
	}

	storage, err := createStatefulSetGuard(func() (*appsv1.StatefulSet, error) {
		return con.getOrCreateStorageStatefulSet(reqLogger, cr)
	})
	if err != nil {
		reqLogger.Error(err, "failed to create storage StatefulSet")
		return nil, nil, err
	}

	// Only update replica number here for scaling out.
	if int32(cr.Spec.ChunkNumber)*2 > *storage.Spec.Replicas {
		err = con.updateStorageStatefulSet(reqLogger, cr, storage)
		if err != nil {
			if err != errRetryReconciliation {
				reqLogger.Error(err, "failed to update storage StatefulSet")
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
		reqLogger.Info("Creating a new storage service", "Name", service.Name)
		err = con.r.client.Create(context.TODO(), service)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				reqLogger.Info("storage service already exists")
			} else {
				reqLogger.Error(err, "failed to create storage service")
			}
			return nil, err
		}

		reqLogger.Info("Successfully created a new storage service", "Name", service.Name)
		return service, nil
	} else if err != nil {
		reqLogger.Error(err, "failed to get storage service")
		return nil, err
	}

	reqLogger.Info("Skip reconcile: storage service already exists", "Name", found.Name)
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
		reqLogger.Info("Creating a new storage StatefulSet", "Name", storage.Name)
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
		reqLogger.Error(err, "failed to get storage StatefulSet")
		return nil, err
	}

	// storage already exists - don't requeue
	reqLogger.Info("Skip reconcile: storage StatefulSet already exists", "Name", found.Name)
	return found, nil
}

func (con *storageController) scaleDownStorageStatefulSet(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, storage *appsv1.StatefulSet, info *clusterInfo) error {
	expectedNodeNumber := int(cr.Spec.ChunkNumber) * chunkNodeNumber
	if info.NodeNumberWithSlots > expectedNodeNumber {
		reqLogger.Info("Need to wait for slot migration to scale down storage")
		return errRetryReconciliation
	}

	if info.NodeNumberWithSlots < expectedNodeNumber {
		reqLogger.Info("Need to scale up")
		return errRetryReconciliation
	}

	replicaNum := int32(int(cr.Spec.ChunkNumber) * chunkShardNumber)
	if *storage.Spec.Replicas == replicaNum {
		return nil
	}

	// Postpone scaling down statefulset to avoid connection reset
	if cr.Status.ScaleState != scaleStateScaleDownWait {
		if err := con.setScaleState(reqLogger, cr, scaleStateScaleDownWait); err != nil {
			return err
		}
		return errRetryReconciliation
	}

	if cr.Status.ScaleDownWaitTimestamp.Add(scaleDownWaitTime).After(time.Now()) {
		reqLogger.Info("Wait extra time before scaling down")
		return errRetryReconciliation
	}

	err := con.updateStorageStatefulSet(reqLogger, cr, storage)
	if err != nil {
		return err
	}

	if err := con.setScaleState(reqLogger, cr, scaleStateStable); err != nil {
		return err
	}

	return nil
}

func (con *storageController) setScaleState(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, scaleState string) error {
	cr.Status.ScaleState = scaleState
	if scaleState == scaleStateScaleDownWait {
		cr.Status.ScaleDownWaitTimestamp = metav1.Now()
	} else {
		cr.Status.ScaleDownWaitTimestamp = metav1.Unix(0, 0)
	}

	// This needs to be accurate so use UPDATE with ResourceVersion check
	// instead of PATCH here.
	err := con.r.client.Status().Update(context.TODO(), cr)
	if err != nil {
		if errors.IsConflict(err) {
			reqLogger.Info("Conflict on updating ScaleState. Try again.", "error", err)
			return errRetryReconciliation
		}
		reqLogger.Error(err, "Failed to change ScaleState",
			"currState", cr.Status.ScaleState,
			"newState", scaleState)
		return err
	}
	return nil
}

func (con *storageController) updateStorageStatefulSet(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, storage *appsv1.StatefulSet) error {
	replicaNum := int32(int(cr.Spec.ChunkNumber) * chunkShardNumber)
	storage.Spec.Replicas = &replicaNum

	if len(storage.ObjectMeta.ResourceVersion) == 0 {
		err := pkgerrors.Errorf("Empty ResourceVersion when updating storage statefulset replicas: %s", cr.ObjectMeta.Name)
		reqLogger.Error(err, "failed to update storage statefulset. Empty ResourceVersion.")
		return err
	}

	err := con.r.client.Update(context.TODO(), storage)
	if err != nil {
		if errors.IsConflict(err) {
			reqLogger.Info("Conflict on updating storage StatefulSet Replicas. Try again.")
			return errRetryReconciliation
		}
		reqLogger.Error(err, "failed to update storage StatefulSet")
		return err
	}

	return nil
}

func (con *storageController) getServiceEndpointsNum(storageService *corev1.Service) (int, error) {
	endpoints, err := getEndpoints(con.r.client, storageService.Name, storageService.Namespace)
	if err != nil {
		return 0, err
	}
	return len(endpoints), nil
}

func (con *storageController) storageAllReady(storageService *corev1.Service, cr *undermoonv1alpha1.Undermoon) (bool, error) {
	// We only expose those server proxies initialized by UMCTL SETCLUSTER to redis clients.
	// That's why we
	// 1. only set the server proxy pods ready when they have received "UMCTL SETCLUSTER".
	// 2. don't set PublishNotReadyAddresses for storage public service,
	// 3. while set PublishNotReadyAddresses for storage service for brokers to access them.
	// We can still know whether the storage pods are successfully created
	// by checking whether they have corresponding Endpoints in storage service (not the public one).
	n, err := con.getServiceEndpointsNum(storageService)
	if err != nil {
		return false, err
	}
	serverProxyNum := int32(int(cr.Spec.ChunkNumber) * chunkShardNumber)
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

	// Check whether Statefulset is scaling.
	// Maybe we can just use storageAllReady instead.
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
		reqLogger.Error(err, "Failed to get endpoints of server proxies")
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
			reqLogger.Error(err, "failed to parse storage hostname")
		}
		address := genStorageFQDNFromName(hostname, cr)
		proxy := newServerProxyMeta(address, address, cr.Spec.Port, int(index))
		proxies = append(proxies, proxy)
	}

	return proxies, nil
}

func (con *storageController) triggerStatefulSetRollingUpdate(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, storageStatefulSet *appsv1.StatefulSet, storageService *corev1.Service) error {
	err := con.updateStatefulSetHelper(reqLogger, cr, storageStatefulSet)
	if err != nil {
		return err
	}

	ready, err := con.storageAllReady(storageService, cr)
	if err != nil {
		reqLogger.Error(err, "Failed to check storage readiness")
		return err
	}
	if !ready {
		reqLogger.Info("storage StatefulSet is not ready after rolling update. Try again.")
		return errRetryReconciliation
	}

	return nil
}

func (con *storageController) updateStatefulSetHelper(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, storageStatefulSet *appsv1.StatefulSet) error {
	newStatefulSet := createStorageStatefulSet(cr)
	storageStatefulSet.Spec = newStatefulSet.Spec

	if len(storageStatefulSet.ObjectMeta.ResourceVersion) == 0 {
		err := pkgerrors.Errorf("Empty ResourceVersion when updating statefulsetStatefulSet: %s", cr.ObjectMeta.Name)
		reqLogger.Error(err, "failed to update storage statefulset. Empty ResourceVersion.")
		return err
	}

	err := con.r.client.Update(context.TODO(), storageStatefulSet)
	if err != nil {
		if errors.IsConflict(err) {
			reqLogger.Info("Conflict on updating storage StatefulSet. Try again.")
			return errRetryReconciliation
		}
		reqLogger.Error(err, "failed to update storage statefulset")
		return err
	}
	reqLogger.Info("Successfully update storage StatefulSet")

	return nil
}

func (con *storageController) needRollingUpdate(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, storageStatefulSet *appsv1.StatefulSet) bool {
	return storageStatefulSetChanged(reqLogger, cr, storageStatefulSet)
}
