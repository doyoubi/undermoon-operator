/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	undermoonv1alpha1 "github.com/doyoubi/undermoon-operator/api/v1alpha1"
)

// NewUndermoonReconciler returns a UndermoonReconciler
func NewUndermoonReconciler(client client.Client, log logr.Logger, scheme *runtime.Scheme) *UndermoonReconciler {
	r := &UndermoonReconciler{
		client: client,
		log:    log,
		scheme: scheme,
	}
	r.brokerCon = newBrokerController(r)
	r.coodinatorCon = newCoordinatorController(r)
	r.storageCon = newStorageController(r)
	r.metaCon = newMetaController()
	return r
}

// UndermoonReconciler reconciles a Undermoon object
type UndermoonReconciler struct {
	client client.Client
	log    logr.Logger
	scheme *runtime.Scheme

	brokerCon     *memBrokerController
	coodinatorCon *coordinatorController
	storageCon    *storageController
	metaCon       *metaController
}

// +kubebuilder:rbac:groups=undermoon.doyoubi.mydomain,resources=undermoons,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=undermoon.doyoubi.mydomain,resources=undermoons/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;

// Reconcile implements Reconciler
func (r *UndermoonReconciler) Reconcile(request ctrl.Request) (ctrl.Result, error) {
	reqLogger := r.log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Undermoon")

	// Fetch the Undermoon instance
	instance := &undermoonv1alpha1.Undermoon{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	resource, err := r.createResources(reqLogger, instance)
	if err != nil {
		if err == errRetryReconciliation {
			return reconcile.Result{Requeue: true, RequeueAfter: 3 * time.Second}, nil
		}
		return reconcile.Result{}, err
	}

	ready, err := r.brokerAndCoordinatorReady(resource, reqLogger, instance)
	if err != nil {
		return reconcile.Result{}, err
	}
	if !ready {
		return reconcile.Result{Requeue: true, RequeueAfter: 3 * time.Second}, nil
	}

	masterBrokerAddress, replicaAddresses, err := r.brokerCon.reconcileMaster(reqLogger, instance, resource.brokerService)
	if err != nil {
		if err == errRetryReconciliation {
			return reconcile.Result{Requeue: true, RequeueAfter: 3 * time.Second}, nil
		}
		return reconcile.Result{}, err
	}

	err = r.coodinatorCon.configSetBroker(reqLogger, instance, resource.coordinatorService, masterBrokerAddress)
	if err != nil {
		return reconcile.Result{}, err
	}

	maxEpochFromServerProxy, err := r.storageCon.getMaxEpoch(reqLogger, resource.storageService, instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = r.metaCon.fixBrokerEpoch(reqLogger, masterBrokerAddress, maxEpochFromServerProxy, instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	proxies, err := r.storageCon.getServerProxies(reqLogger, resource.storageService, instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	storageAllReady, err := r.storageCon.storageAllReady(resource.storageService, instance)
	if err != nil {
		return reconcile.Result{}, err
	}

	info, err := r.metaCon.reconcileMeta(reqLogger, masterBrokerAddress, replicaAddresses, proxies, instance, storageAllReady)
	if err != nil {
		if err == errRetryReconciliation {
			return reconcile.Result{Requeue: true, RequeueAfter: 3 * time.Second}, nil
		}
		return reconcile.Result{}, err
	}

	// Before scaling, we need to wait for those TERMINATING pods to be killed completely.
	storageAllReadyAndStable, err := r.storageCon.storageAllReadyAndStable(resource.storageService, resource.storageStatefulSet, instance)
	if err != nil {
		return reconcile.Result{}, err
	}
	if !storageAllReadyAndStable {
		return reconcile.Result{Requeue: true, RequeueAfter: 10 * time.Second}, nil
	}

	err = r.metaCon.changeMeta(reqLogger, masterBrokerAddress, instance, info)
	if err != nil {
		if err == errRetryReconciliation {
			return reconcile.Result{Requeue: true, RequeueAfter: 3 * time.Second}, nil
		}
		return reconcile.Result{}, err
	}

	// Ignore the proxies fetched from service.
	proxies = []serverProxyMeta{}
	info, err = r.metaCon.reconcileMeta(reqLogger, masterBrokerAddress, replicaAddresses, proxies, instance, storageAllReady)
	if err != nil {
		if err == errRetryReconciliation {
			return reconcile.Result{Requeue: true, RequeueAfter: 3 * time.Second}, nil
		}
		return reconcile.Result{}, err
	}

	resource.storageStatefulSet, err = r.storageCon.scaleDownStorageStatefulSet(reqLogger, instance, resource.storageStatefulSet, info)
	if err != nil {
		if err == errRetryReconciliation {
			return reconcile.Result{Requeue: true, RequeueAfter: 3 * time.Second}, nil
		}
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

type umResource struct {
	brokerStatefulSet      *appsv1.StatefulSet
	coordinatorStatefulSet *appsv1.StatefulSet
	storageStatefulSet     *appsv1.StatefulSet
	brokerService          *corev1.Service
	coordinatorService     *corev1.Service
	storageService         *corev1.Service
}

func (r *UndermoonReconciler) createResources(reqLogger logr.Logger, instance *undermoonv1alpha1.Undermoon) (*umResource, error) {
	brokerStatefulSet, brokerService, err := r.brokerCon.createBroker(reqLogger, instance)
	if err != nil {
		reqLogger.Error(err, "failed to create broker", "Name", instance.ObjectMeta.Name, "ClusterName", instance.Spec.ClusterName)
		return nil, err
	}

	coordinatorStatefulSet, coordinatorService, err := r.coodinatorCon.createCoordinator(reqLogger, instance)
	if err != nil {
		reqLogger.Error(err, "failed to create coordinator", "Name", instance.ObjectMeta.Name, "ClusterName", instance.Spec.ClusterName)
		return nil, err
	}

	storageStatefulSet, storageService, err := r.storageCon.createStorage(reqLogger, instance)
	if err != nil {
		reqLogger.Error(err, "failed to create storage", "Name", instance.ObjectMeta.Name, "ClusterName", instance.Spec.ClusterName)
		return nil, err
	}

	return &umResource{
		brokerStatefulSet:      brokerStatefulSet,
		coordinatorStatefulSet: coordinatorStatefulSet,
		storageStatefulSet:     storageStatefulSet,
		brokerService:          brokerService,
		coordinatorService:     coordinatorService,
		storageService:         storageService,
	}, nil
}

func (r *UndermoonReconciler) brokerAndCoordinatorReady(resource *umResource, reqLogger logr.Logger, instance *undermoonv1alpha1.Undermoon) (bool, error) {
	ready, err := r.brokerCon.brokerReady(resource.brokerStatefulSet, resource.brokerService)
	if err != nil {
		reqLogger.Error(err, "failed to check broker ready", "Name", instance.ObjectMeta.Name, "ClusterName", instance.Spec.ClusterName)
		return false, err
	}
	if !ready {
		reqLogger.Info("broker statefulset not ready", "Name", instance.ObjectMeta.Name, "ClusterName", instance.Spec.ClusterName)
		return false, nil
	}

	ready, err = r.coodinatorCon.coordinatorReady(resource.coordinatorStatefulSet, resource.coordinatorService)
	if err != nil {
		reqLogger.Error(err, "failed to check coordinator ready", "Name", instance.ObjectMeta.Name, "ClusterName", instance.Spec.ClusterName)
		return false, err
	}
	if !ready {
		reqLogger.Info("coordinator statefulset not ready", "Name", instance.ObjectMeta.Name, "ClusterName", instance.Spec.ClusterName)
		return false, nil
	}

	return true, nil
}

// SetupWithManager setups the controller
func (r *UndermoonReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&undermoonv1alpha1.Undermoon{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Pod{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
