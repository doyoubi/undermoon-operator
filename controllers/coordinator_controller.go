package controllers

import (
	"context"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
	"github.com/go-logr/logr"
	pkgerrors "github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type coordinatorController struct {
	r         *UndermoonReconciler
	coordPool *coordinatorClientPool
}

func newCoordinatorController(r *UndermoonReconciler) *coordinatorController {
	coordPool := newCoordinatorClientPool()
	return &coordinatorController{r: r, coordPool: coordPool}
}

func (con *coordinatorController) createCoordinator(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*appsv1.StatefulSet, *corev1.Service, error) {
	coordinatorService, err := createServiceGuard(func() (*corev1.Service, error) {
		return con.getOrCreateCoordinatorService(reqLogger, cr)
	})
	if err != nil {
		reqLogger.Error(err, "failed to create coordinator service", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return nil, nil, err
	}

	coordinatorStatefulSet, err := createStatefulSetGuard(func() (*appsv1.StatefulSet, error) {
		return con.getOrCreateCoordinatorStatefulSet(reqLogger, cr)
	})
	if err != nil {
		reqLogger.Error(err, "failed to create coordinator statefulset", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return nil, nil, err
	}

	return coordinatorStatefulSet, coordinatorService, nil
}

func (con *coordinatorController) getOrCreateCoordinatorService(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*corev1.Service, error) {
	service := createCoordinatorService(cr)

	if err := controllerutil.SetControllerReference(cr, service, con.r.scheme); err != nil {
		return nil, err
	}

	found := &corev1.Service{}
	err := con.r.client.Get(context.TODO(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new coordinator service", "Namespace", service.Namespace, "Name", service.Name)
		err = con.r.client.Create(context.TODO(), service)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				reqLogger.Info("coordinator service already exists")
			} else {
				reqLogger.Error(err, "failed to create coordinator service")
			}
			return nil, err
		}

		reqLogger.Info("Successfully created a new coordinator service", "Namespace", service.Namespace, "Name", service.Name)
		return service, nil
	} else if err != nil {
		reqLogger.Error(err, "failed to get coordinator service")
		return nil, err
	}

	reqLogger.Info("Skip reconcile: coordinator service already exists", "Namespace", found.Namespace, "Name", found.Name)
	return found, nil
}

func (con *coordinatorController) getOrCreateCoordinatorStatefulSet(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon) (*appsv1.StatefulSet, error) {
	coordinator := createCoordinatorStatefulSet(cr)

	if err := controllerutil.SetControllerReference(cr, coordinator, con.r.scheme); err != nil {
		reqLogger.Error(err, "SetControllerReference failed")
		return nil, err
	}

	// Check if this coordinator Statefulset already exists
	found := &appsv1.StatefulSet{}
	err := con.r.client.Get(context.TODO(), types.NamespacedName{Name: coordinator.Name, Namespace: coordinator.Namespace}, found)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new coordinator statefulset", "Namespace", coordinator.Namespace, "Name", coordinator.Name)
		err = con.r.client.Create(context.TODO(), coordinator)
		if err != nil {
			if errors.IsAlreadyExists(err) {
				reqLogger.Info("coordinator statefulset already exists")
			} else {
				reqLogger.Error(err, "failed to create coordinator statefulset")
			}
			return nil, err
		}

		// Statefulset created successfully - don't requeue
		return coordinator, nil
	} else if err != nil {
		reqLogger.Error(err, "failed to get coordinator statefulset")
		return nil, err
	}

	// coordinator already exists - don't requeue
	reqLogger.Info("Skip reconcile: coordinator statefulset already exists", "Namespace", found.Namespace, "Name", found.Name)
	return found, nil
}

func (con *coordinatorController) getServiceEndpointsNum(coordinatorService *corev1.Service) (int, error) {
	endpoints, err := getEndpoints(con.r.client, coordinatorService.Name, coordinatorService.Namespace)
	if err != nil {
		return 0, err
	}
	return len(endpoints), nil
}

func (con *coordinatorController) coordinatorReady(coordinatorStatefulSet *appsv1.StatefulSet, coordinatorService *corev1.Service) (bool, error) {
	n, err := con.getServiceEndpointsNum(coordinatorService)
	if err != nil {
		return false, err
	}
	ready := coordinatorStatefulSet.Status.ReadyReplicas >= 1 && n >= 1
	return ready, nil
}

func (con *coordinatorController) coordiantorAllReady(coordinatorStatefulSet *appsv1.StatefulSet, coordinatorService *corev1.Service) (bool, error) {
	n, err := con.getServiceEndpointsNum(coordinatorService)
	if err != nil {
		return false, err
	}
	ready := coordinatorStatefulSet.Status.ReadyReplicas >= coordinatorNum && n >= int(coordinatorNum)
	return ready, nil
}

func (con *coordinatorController) configSetBroker(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, coordinatorService *corev1.Service, masterBrokerAddress string) error {
	endpoints, err := getEndpoints(con.r.client, coordinatorService.Name, coordinatorService.Namespace)
	if err != nil {
		reqLogger.Error(err, "failed to get coordinator endpoints", "Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return err
	}

	for _, endpoint := range endpoints {
		address := genCoordinatorAddressFromName(endpoint.Hostname, cr)
		err = con.coordPool.setBrokerAddress(address, masterBrokerAddress)
		if err != nil {
			reqLogger.Error(err, "failed to set broker to coodinator",
				"coordinatorAddress", address,
				"Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		}
	}
	return err
}

func (con *coordinatorController) triggerStatefulSetRollingUpdate(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, coordinatorStatefulSet *appsv1.StatefulSet, coordinatorService *corev1.Service) error {
	err := con.updateStatefulSetHelper(reqLogger, cr, coordinatorStatefulSet)
	if err != nil {
		return err
	}

	ready, err := con.coordiantorAllReady(coordinatorStatefulSet, coordinatorService)
	if err != nil {
		reqLogger.Error(err, "Failed to check coordinator readiness",
			"Name", cr.ObjectMeta.Name, "ClusterName", cr.Spec.ClusterName)
		return err
	}
	if !ready {
		reqLogger.Info("coordinator StatefulSet is not ready after rolling update. Try again.")
		return errRetryReconciliation
	}

	return nil
}

func (con *coordinatorController) updateStatefulSetHelper(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, coordinatorStatefulSet *appsv1.StatefulSet) error {
	newStatefulSet := createCoordinatorStatefulSet(cr)
	coordinatorStatefulSet.Spec = newStatefulSet.Spec

	if len(coordinatorStatefulSet.ObjectMeta.ResourceVersion) == 0 {
		err := pkgerrors.Errorf("Empty ResourceVersion when updating coordinatorStatefulSet: %s", cr.ObjectMeta.Name)
		reqLogger.Error(err, "failed to update coordiantor statefulset. Empty ResourceVersion.",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}

	err := con.r.client.Update(context.TODO(), coordinatorStatefulSet)
	if err != nil {
		if errors.IsConflict(err) {
			reqLogger.Info("Conflict on updating coordiantor StatefulSet. Try again.")
			return errRetryReconciliation
		}
		reqLogger.Error(err, "failed to update coordiantor statefulset",
			"Name", cr.ObjectMeta.Name,
			"ClusterName", cr.Spec.ClusterName)
		return err
	}
	reqLogger.Info("Successfully update coordiantor StatefulSet")

	return nil
}

func (con *coordinatorController) needRollingUpdate(reqLogger logr.Logger, cr *undermoonv1alpha1.Undermoon, coordinatorStatefulSet *appsv1.StatefulSet) bool {
	return coordinatorStatefulSetChanged(reqLogger, cr, coordinatorStatefulSet)
}
