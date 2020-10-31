package controllers

import (
	"context"
	"fmt"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	pkgerrors "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type memBrokerController struct {
	r      *UndermoonReconciler
	client *brokerClient
}

func newBrokerController(r *UndermoonReconciler) *memBrokerController {
	client := newBrokerClient()
	return &memBrokerController{r: r, client: client}
}

func (con *memBrokerController) createBroker(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*appsv1.StatefulSet, *corev1.Service, error) {
	brokerService, err := createServiceGuard(func() (*corev1.Service, error) {
		return con.getOrCreateBrokerService(reqLogger, cr)
	})
	if err != nil {
		reqLogger.Error(err, "failed to create broker service")
		return nil, nil, err
	}

	brokerStatefulSet, err := createStatefulSetGuard(func() (*appsv1.StatefulSet, error) {
		return con.getOrCreateBrokerStatefulSet(reqLogger, cr)
	})
	if err != nil {
		reqLogger.Error(err, "failed to create broker statefulset")
		return nil, nil, err
	}

	return brokerStatefulSet, brokerService, nil
}

func (con *memBrokerController) getOrCreateBrokerService(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*corev1.Service, error) {
	service := createBrokerService(cr)

	if err := controllerutil.SetControllerReference(cr, service, con.r.scheme); err != nil {
		return nil, err
	}

	found := &corev1.Service{}
	err := con.r.client.Get(context.TODO(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new broker service", "Name", service.Name)
		err = con.r.client.Create(context.TODO(), service)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				reqLogger.Info("broker service already exists")
			} else {
				reqLogger.Error(err, "failed to create broker service")
			}
			return nil, err
		}

		reqLogger.Info("Successfully created a new broker service", "Name", service.Name)
		return service, nil
	} else if err != nil {
		reqLogger.Error(err, "failed to get broker service")
		return nil, err
	}

	reqLogger.Info("Skip reconcile: broker service already exists", "Name", found.Name)
	return found, nil
}

func (con *memBrokerController) getOrCreateBrokerStatefulSet(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*appsv1.StatefulSet, error) {
	broker := createBrokerStatefulSet(cr)

	if err := controllerutil.SetControllerReference(cr, broker, con.r.scheme); err != nil {
		reqLogger.Error(err, "SetControllerReference failed")
		return nil, err
	}

	// Check if this broker Statefulset already exists
	found := &appsv1.StatefulSet{}
	err := con.r.client.Get(context.TODO(), types.NamespacedName{Name: broker.Name, Namespace: broker.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new broker statefulset", "Name", broker.Name)
		err = con.r.client.Create(context.TODO(), broker)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				reqLogger.Info("broker statefulset already exists")
			} else {
				reqLogger.Error(err, "failed to create broker statefulset")
			}
			return nil, err
		}
		// Statefulset created successfully - don't requeue
		return broker, nil
	} else if err != nil {
		reqLogger.Error(err, "failed to get broker statefulset")
		return nil, err
	}

	// broker already exists - don't requeue
	reqLogger.Info("Skip reconcile: broker statefulset already exists", "Name", found.Name)
	return found, nil
}

func (con *memBrokerController) getServiceEndpointsNum(brokerService *corev1.Service) (int, error) {
	endpoints, err := getEndpoints(con.r.client, brokerService.Name, brokerService.Namespace)
	if err != nil {
		return 0, err
	}
	return len(endpoints), nil
}

func (con *memBrokerController) brokerReady(brokerStatefulSet *appsv1.StatefulSet, brokerService *corev1.Service) (bool, error) {
	n, err := con.getServiceEndpointsNum(brokerService)
	if err != nil {
		return false, err
	}
	ready := brokerStatefulSet.Status.ReadyReplicas >= brokerNum-1 && n >= int(brokerNum-1)
	return ready, nil
}

func (con *memBrokerController) brokerAllReady(brokerStatefulSet *appsv1.StatefulSet, brokerService *corev1.Service) (bool, error) {
	n, err := con.getServiceEndpointsNum(brokerService)
	if err != nil {
		return false, err
	}
	ready := brokerStatefulSet.Status.ReadyReplicas == brokerNum && n >= int(brokerNum)
	return ready, nil
}

func (con *memBrokerController) reconcileMaster(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, brokerService *corev1.Service) (string, []string, error) {
	endpoints, err := getEndpoints(con.r.client, brokerService.Name, brokerService.Namespace)
	if err != nil {
		reqLogger.Error(err, "failed to get broker endpoints")
		return "", nil, err
	}
	brokerAddresses := make([]string, 0)
	for _, endpoint := range endpoints {
		addr := genBrokerAddressFromName(endpoint.Hostname, cr)
		brokerAddresses = append(brokerAddresses, addr)
	}

	currMaster, err := con.getCurrentMaster(reqLogger, brokerAddresses)
	if err != nil {
		reqLogger.Error(err, "failed to get current master")
		return "", nil, err
	}
	err = con.setMasterBrokerStatus(reqLogger, cr, currMaster)
	if err != nil {
		return "", nil, err
	}

	replicaAddresses := make([]string, 0)
	for _, address := range brokerAddresses {
		if address == currMaster {
			continue
		}
		replicaAddresses = append(replicaAddresses, address)
	}

	return currMaster, replicaAddresses, nil
}

func (con *memBrokerController) setMasterBrokerStatus(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, masterBrokerAddress string) error {
	// `masterBrokerAddress` is only exposed to external for debugging.
	// It does not need to be accurate so use PATCH here.
	patchData := []byte(fmt.Sprintf(`{"status":{"masterBrokerAddress":"%s"}}`, masterBrokerAddress))
	patch := client.RawPatch(types.MergePatchType, patchData)

	err := con.r.client.Status().Patch(context.TODO(), cr, patch)
	if err != nil {
		reqLogger.Error(err, "Failed to set master broker address")
		return err
	}
	return nil
}

func (con *memBrokerController) getCurrentMaster(reqLogger logr.Logger, brokerAddresses []string) (string, error) {
	if len(brokerAddresses) == 0 {
		return "", pkgerrors.Errorf("broker addresses is empty")
	}

	masterBrokers := []string{}
	for _, address := range brokerAddresses {
		replicaAddresses, err := con.client.getReplicaAddresses(address)
		if err != nil {
			reqLogger.Error(err, "failed to get replica addresses from broker", "address", address)
			continue
		}
		if len(replicaAddresses) != 0 {
			masterBrokers = append(masterBrokers, address)
		}
	}

	if len(masterBrokers) == 1 {
		return masterBrokers[0], nil
	}

	if len(masterBrokers) == 0 {
		masterBrokers = append(masterBrokers, brokerAddresses...)
	}

	var maxEpoch int64 = 0
	maxEpochBroker := ""
	for _, address := range masterBrokers {
		epoch, err := con.client.getEpoch(address)
		if err != nil {
			reqLogger.Error(err, "failed to get epoch from broker", "address", address)
			continue
		}
		if maxEpochBroker == "" || epoch > maxEpoch {
			maxEpochBroker = address
			maxEpoch = epoch
		}
	}

	return maxEpochBroker, nil
}

func (con *memBrokerController) triggerStatefulSetRollingUpdate(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, brokerStatefulSet *appsv1.StatefulSet, brokerService *corev1.Service) error {
	err := con.updateStatefulSetHelper(reqLogger, cr, brokerStatefulSet)
	if err != nil {
		return err
	}

	ready, err := con.brokerAllReady(brokerStatefulSet, brokerService)
	if err != nil {
		reqLogger.Error(err, "Failed to check broker readiness",
			"Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return err
	}
	if !ready {
		reqLogger.Info("broker StatefulSet is not ready after rolling update. Try again.")
		return errRetryReconciliation
	}

	return nil
}

func (con *memBrokerController) updateStatefulSetHelper(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, brokerStatefulSet *appsv1.StatefulSet) error {
	newStatefulSet := createBrokerStatefulSet(cr)
	brokerStatefulSet.Spec = newStatefulSet.Spec

	if len(brokerStatefulSet.ObjectMeta.ResourceVersion) == 0 {
		err := pkgerrors.Errorf("Empty ResourceVersion when updating brokerStatefulset: %s", cr.ObjectMeta.Name)
		reqLogger.Error(err, "failed to update broker statefulset. Empty ResourceVersion.")
		return err
	}

	err := con.r.client.Update(context.TODO(), brokerStatefulSet)
	if err != nil {
		if errors.IsConflict(err) {
			reqLogger.Info("Conflict on updating broker StatefulSet. Try again.")
			return errRetryReconciliation
		}
		reqLogger.Error(err, "failed to update broker statefulset")
		return err
	}
	reqLogger.Info("Successfully update broker StatefulSet")

	return nil
}

func (con *memBrokerController) needRollingUpdate(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, brokerStatefulSet *appsv1.StatefulSet) bool {
	return brokerStatefulSetChanged(reqLogger, cr, brokerStatefulSet)
}
