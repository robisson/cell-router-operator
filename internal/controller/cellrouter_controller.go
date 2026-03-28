package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"k8s.io/client-go/tools/record"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"
	routerresource "github.com/robisson/cell-router-operator/internal/resource/router"
)

const (
	requeueDelay = 10 * time.Second
)

// CellRouterReconciler reconciles a CellRouter object.
type CellRouterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=cell.cellrouter.io,resources=cellrouters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cell.cellrouter.io,resources=cellrouters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cell.cellrouter.io,resources=cellrouters/finalizers,verbs=update
// +kubebuilder:rbac:groups=cell.cellrouter.io,resources=cells,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways/status,verbs=get
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes/status,verbs=get
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=referencegrants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile aligns cluster state with the CellRouter specification.
func (r *CellRouterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("cellRouter", req.Name)

	var router cellv1alpha1.CellRouter
	if err := r.Get(ctx, req.NamespacedName, &router); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !router.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, &router, logger)
	}

	if err := r.ensureFinalizer(ctx, &router); err != nil {
		logger.Error(err, "failed to ensure finalizer")
		return ctrl.Result{}, err
	}

	return r.reconcileNormal(ctx, &router, logger)
}

func (r *CellRouterReconciler) reconcileNormal(ctx context.Context, router *cellv1alpha1.CellRouter, logger logr.Logger) (ctrl.Result, error) {
	statusBase := router.DeepCopy()
	gatewayNamespace := router.Spec.Gateway.Namespace

	if err := r.reconcileGatewayNamespace(ctx, router, gatewayNamespace); err != nil {
		logger.Error(err, "failed to reconcile gateway namespace")
		r.setRouterCondition(router, cellv1alpha1.CellRouterGatewayReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, err.Error())
		r.setRouterCondition(router, cellv1alpha1.CellRouterReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, "gateway namespace reconciliation failed")
		_ = r.patchRouterStatus(ctx, router, statusBase)
		return ctrl.Result{}, err
	}

	gatewayRef, err := r.reconcileGateway(ctx, router)
	if err != nil {
		logger.Error(err, "failed to reconcile gateway")
		r.setRouterCondition(router, cellv1alpha1.CellRouterGatewayReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, err.Error())
		r.setRouterCondition(router, cellv1alpha1.CellRouterReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, "gateway reconciliation failed")
		_ = r.patchRouterStatus(ctx, router, statusBase)
		return ctrl.Result{}, err
	}
	r.setRouterCondition(router, cellv1alpha1.CellRouterGatewayReadyCondition, metav1.ConditionTrue, cellv1alpha1.ConditionReasonReconciled, "gateway reconciled")

	routesReady, needsRequeue, managedRoutes, err := r.reconcileRoutes(ctx, router, gatewayNamespace, logger)
	if err != nil {
		logger.Error(err, "failed to reconcile routes")
		r.setRouterCondition(router, cellv1alpha1.CellRouterRoutesReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, err.Error())
		r.setRouterCondition(router, cellv1alpha1.CellRouterReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, "route reconciliation failed")
		_ = r.patchRouterStatus(ctx, router, statusBase)
		return ctrl.Result{}, err
	}

	router.Status.ManagedGatewayRef = gatewayRef
	router.Status.ManagedRoutes = managedRoutes
	router.Status.ObservedGeneration = router.Generation

	if routesReady {
		r.setRouterCondition(router, cellv1alpha1.CellRouterRoutesReadyCondition, metav1.ConditionTrue, cellv1alpha1.ConditionReasonReconciled, "routes reconciled")
		r.setRouterCondition(router, cellv1alpha1.CellRouterReadyCondition, metav1.ConditionTrue, cellv1alpha1.ConditionReasonReconciled, "router is ready")
	} else {
		r.setRouterCondition(router, cellv1alpha1.CellRouterRoutesReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonProgressing, "waiting for routes to become ready")
		r.setRouterCondition(router, cellv1alpha1.CellRouterReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonProgressing, "router is waiting for routes to become ready")
	}

	if err := r.patchRouterStatus(ctx, router, statusBase); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	if needsRequeue {
		return ctrl.Result{RequeueAfter: requeueDelay}, nil
	}

	return ctrl.Result{}, nil
}

func (r *CellRouterReconciler) reconcileDeletion(ctx context.Context, router *cellv1alpha1.CellRouter, logger logr.Logger) (ctrl.Result, error) {
	gatewayNamespace := router.Spec.Gateway.Namespace

	if err := r.deleteManagedHTTPRoutes(ctx, router, gatewayNamespace); err != nil {
		logger.Error(err, "failed to delete managed routes during finalization")
		return ctrl.Result{}, err
	}

	if err := r.deleteManagedReferenceGrants(ctx, router); err != nil {
		logger.Error(err, "failed to delete managed reference grants during finalization")
		return ctrl.Result{}, err
	}

	if err := r.deleteManagedGateway(ctx, router); err != nil {
		logger.Error(err, "failed to delete managed gateway during finalization")
		return ctrl.Result{}, err
	}

	if controllerutil.ContainsFinalizer(router, constants.FinalizerCellRouter) {
		patch := client.MergeFrom(router.DeepCopy())
		controllerutil.RemoveFinalizer(router, constants.FinalizerCellRouter)
		if err := r.Patch(ctx, router, patch); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *CellRouterReconciler) ensureFinalizer(ctx context.Context, router *cellv1alpha1.CellRouter) error {
	if controllerutil.ContainsFinalizer(router, constants.FinalizerCellRouter) {
		return nil
	}

	patch := client.MergeFrom(router.DeepCopy())
	controllerutil.AddFinalizer(router, constants.FinalizerCellRouter)
	return r.Patch(ctx, router, patch)
}

func (r *CellRouterReconciler) reconcileGatewayNamespace(ctx context.Context, router *cellv1alpha1.CellRouter, namespace string) error {
	if namespace == "" {
		return fmt.Errorf("gateway namespace is required")
	}

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ns, func() error {
		routerresource.MutateGatewayNamespace(ns, router)
		return nil
	})
	if err == nil && r.Recorder != nil {
		r.Recorder.Eventf(router, corev1.EventTypeNormal, "GatewayNamespaceReconciled", "Gateway namespace %s reconciled", namespace)
	}
	return err
}

func (r *CellRouterReconciler) reconcileGateway(ctx context.Context, router *cellv1alpha1.CellRouter) (string, error) {
	gateway := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{
		Name:      router.Spec.Gateway.Name,
		Namespace: router.Spec.Gateway.Namespace,
	}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, gateway, func() error {
		routerresource.MutateGateway(gateway, router)
		return controllerutil.SetControllerReference(router, gateway, r.Scheme)
	})
	if err != nil {
		return "", err
	}

	if r.Recorder != nil {
		r.Recorder.Eventf(router, corev1.EventTypeNormal, "GatewayReconciled", "Gateway %s/%s reconciled", gateway.Namespace, gateway.Name)
	}

	return fmt.Sprintf("%s/%s", gateway.Namespace, gateway.Name), nil
}

func (r *CellRouterReconciler) reconcileRoutes(ctx context.Context, router *cellv1alpha1.CellRouter, gatewayNamespace string, logger logr.Logger) (allReady bool, needsRequeue bool, statuses []cellv1alpha1.ManagedRouteStatus, err error) {
	expected := make(map[string]struct{}, len(router.Spec.Routes))
	expectedGrants := make(map[string]struct{}, len(router.Spec.Routes))
	statuses = make([]cellv1alpha1.ManagedRouteStatus, 0, len(router.Spec.Routes))
	allReady = true
	needsRequeue = false

	for _, routeSpec := range router.Spec.Routes {
		expected[routeSpec.Name] = struct{}{}
		expectedGrants[routerresource.ReferenceGrantName(routeSpec.Name)] = struct{}{}

		cell := &cellv1alpha1.Cell{}
		if err := r.Get(ctx, types.NamespacedName{Name: routeSpec.CellRef}, cell); err != nil {
			if apierrors.IsNotFound(err) {
				return false, true, statuses, fmt.Errorf("referenced cell %q not found", routeSpec.CellRef)
			}
			return false, true, statuses, err
		}

		backend, backendErr := r.resolveBackend(cell)
		if backendErr != nil {
			return false, true, statuses, fmt.Errorf("failed to resolve backend for cell %q: %w", routeSpec.CellRef, backendErr)
		}
		backend.Weight = routeSpec.Weight

		if err := r.reconcileReferenceGrant(ctx, router, routeSpec, gatewayNamespace, backend); err != nil {
			return false, true, statuses, err
		}

		httpRoute := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{
			Name:      routeSpec.Name,
			Namespace: gatewayNamespace,
		}}

		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, httpRoute, func() error {
			routerresource.MutateHTTPRoute(httpRoute, router, routeSpec, gatewayNamespace, backend)
			return controllerutil.SetControllerReference(router, httpRoute, r.Scheme)
		})
		if err != nil {
			return false, true, statuses, err
		}

		if r.Recorder != nil {
			r.Recorder.Eventf(router, corev1.EventTypeNormal, "HTTPRouteReconciled", "HTTPRoute %s/%s reconciled", httpRoute.Namespace, httpRoute.Name)
		}

		routeReady, readyTime := isRouteReady(httpRoute, router.Spec.Gateway.Name, gatewayNamespace)
		if !routeReady {
			allReady = false
			needsRequeue = true
		}

		if cellCond := apimeta.FindStatusCondition(cell.Status.Conditions, cellv1alpha1.CellReadyCondition); cellCond == nil || cellCond.Status != metav1.ConditionTrue {
			allReady = false
			needsRequeue = true
			logger.Info("referenced cell is not ready yet", "cell", cell.Name)
		}

		routeStatus := cellv1alpha1.ManagedRouteStatus{
			Name:          routeSpec.Name,
			ListenerNames: routeSpec.ListenerNames,
			CellRef:       routeSpec.CellRef,
		}
		if readyTime != nil {
			copy := *readyTime
			routeStatus.LastTransitionTime = &copy
		}
		statuses = append(statuses, routeStatus)
	}

	if err := r.cleanupStaleRoutes(ctx, router, gatewayNamespace, expected); err != nil {
		return false, true, statuses, err
	}

	if err := r.cleanupStaleReferenceGrants(ctx, router, expectedGrants); err != nil {
		return false, true, statuses, err
	}

	return allReady, needsRequeue, statuses, nil
}

func isRouteReady(httpRoute *gatewayv1.HTTPRoute, gatewayName, gatewayNamespace string) (bool, *metav1.Time) {
	for _, parent := range httpRoute.Status.Parents {
		if string(parent.ParentRef.Name) != gatewayName {
			continue
		}
		if parent.ParentRef.Namespace != nil && string(*parent.ParentRef.Namespace) != gatewayNamespace {
			continue
		}

		var accepted bool
		var resolved bool
		var resolvedSeen bool
		var readyTime *metav1.Time

		for _, cond := range parent.Conditions {
			switch cond.Type {
			case string(gatewayv1.RouteConditionAccepted):
				if cond.Status == metav1.ConditionTrue {
					accepted = true
					t := cond.LastTransitionTime
					readyTime = &t
				}
			case string(gatewayv1.RouteConditionResolvedRefs):
				resolvedSeen = true
				if cond.Status == metav1.ConditionTrue {
					resolved = true
					if readyTime == nil || cond.LastTransitionTime.After(readyTime.Time) {
						t := cond.LastTransitionTime
						readyTime = &t
					}
				}
			}
		}

		if accepted && (!resolvedSeen || resolved) {
			return true, readyTime
		}
	}
	return false, nil
}

func (r *CellRouterReconciler) reconcileReferenceGrant(ctx context.Context, router *cellv1alpha1.CellRouter, routeSpec cellv1alpha1.CellRouteSpec, gatewayNamespace string, backend routerresource.BackendTarget) error {
	grant := &gatewayv1beta1.ReferenceGrant{ObjectMeta: metav1.ObjectMeta{
		Name:      routerresource.ReferenceGrantName(routeSpec.Name),
		Namespace: backend.Namespace,
	}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, grant, func() error {
		routerresource.MutateReferenceGrant(grant, router, routeSpec, gatewayNamespace, backend)
		return controllerutil.SetControllerReference(router, grant, r.Scheme)
	})
	if err == nil && r.Recorder != nil {
		r.Recorder.Eventf(router, corev1.EventTypeNormal, "ReferenceGrantReconciled", "ReferenceGrant %s/%s reconciled", grant.Namespace, grant.Name)
	}
	return err
}

func (r *CellRouterReconciler) cleanupStaleRoutes(ctx context.Context, router *cellv1alpha1.CellRouter, gatewayNamespace string, expected map[string]struct{}) error {
	var existing gatewayv1.HTTPRouteList
	if err := r.List(ctx, &existing,
		client.InNamespace(gatewayNamespace),
		client.MatchingLabels{
			constants.ManagedByLabel:  constants.OperatorName,
			constants.RouterNameLabel: router.Name,
		},
	); err != nil {
		return err
	}

	for idx := range existing.Items {
		route := existing.Items[idx]
		if _, ok := expected[route.Name]; ok {
			continue
		}

		if err := client.IgnoreNotFound(r.Delete(ctx, route.DeepCopy())); err != nil {
			return err
		}
	}

	return nil
}

func (r *CellRouterReconciler) cleanupStaleReferenceGrants(ctx context.Context, router *cellv1alpha1.CellRouter, expected map[string]struct{}) error {
	var existing gatewayv1beta1.ReferenceGrantList
	if err := r.List(ctx, &existing,
		client.MatchingLabels{
			constants.ManagedByLabel:  constants.OperatorName,
			constants.RouterNameLabel: router.Name,
		},
	); err != nil {
		return err
	}

	for idx := range existing.Items {
		grant := existing.Items[idx]
		if _, ok := expected[grant.Name]; ok {
			continue
		}

		if err := client.IgnoreNotFound(r.Delete(ctx, grant.DeepCopy())); err != nil {
			return err
		}
	}

	return nil
}

func (r *CellRouterReconciler) resolveBackend(cell *cellv1alpha1.Cell) (routerresource.BackendTarget, error) {
	namespace := cell.Status.Namespace
	if namespace == "" {
		namespace = effectiveNamespace(cell)
	}
	if namespace == "" {
		return routerresource.BackendTarget{}, fmt.Errorf("cell namespace is not available")
	}

	serviceName := cell.Spec.Entrypoint.ServiceName
	if serviceName == "" {
		return routerresource.BackendTarget{}, fmt.Errorf("cell entrypoint service name is empty")
	}

	if cell.Spec.Entrypoint.Port == 0 {
		return routerresource.BackendTarget{}, fmt.Errorf("cell entrypoint port is not set")
	}

	return routerresource.BackendTarget{
		Namespace: namespace,
		Name:      serviceName,
		Port:      cell.Spec.Entrypoint.Port,
	}, nil
}

func (r *CellRouterReconciler) deleteManagedHTTPRoutes(ctx context.Context, router *cellv1alpha1.CellRouter, namespace string) error {
	var routes gatewayv1.HTTPRouteList
	if err := r.List(ctx, &routes,
		client.InNamespace(namespace),
		client.MatchingLabels{
			constants.ManagedByLabel:  constants.OperatorName,
			constants.RouterNameLabel: router.Name,
		},
	); err != nil {
		return err
	}

	for idx := range routes.Items {
		route := routes.Items[idx]
		if !metav1.IsControlledBy(&route, router) {
			continue
		}
		if err := client.IgnoreNotFound(r.Delete(ctx, route.DeepCopy())); err != nil {
			return err
		}
	}

	return nil
}

func (r *CellRouterReconciler) deleteManagedGateway(ctx context.Context, router *cellv1alpha1.CellRouter) error {
	gateway := &gatewayv1.Gateway{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      router.Spec.Gateway.Name,
		Namespace: router.Spec.Gateway.Namespace,
	}, gateway)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if !metav1.IsControlledBy(gateway, router) {
		return nil
	}

	return client.IgnoreNotFound(r.Delete(ctx, gateway))
}

func (r *CellRouterReconciler) deleteManagedReferenceGrants(ctx context.Context, router *cellv1alpha1.CellRouter) error {
	var grants gatewayv1beta1.ReferenceGrantList
	if err := r.List(ctx, &grants,
		client.MatchingLabels{
			constants.ManagedByLabel:  constants.OperatorName,
			constants.RouterNameLabel: router.Name,
		},
	); err != nil {
		return err
	}

	for idx := range grants.Items {
		grant := grants.Items[idx]
		if !metav1.IsControlledBy(&grant, router) {
			continue
		}
		if err := client.IgnoreNotFound(r.Delete(ctx, grant.DeepCopy())); err != nil {
			return err
		}
	}

	return nil
}

func (r *CellRouterReconciler) patchRouterStatus(ctx context.Context, router *cellv1alpha1.CellRouter, base *cellv1alpha1.CellRouter) error {
	return r.Status().Patch(ctx, router, client.MergeFrom(base))
}

func (r *CellRouterReconciler) setRouterCondition(router *cellv1alpha1.CellRouter, condType string, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&router.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: router.Generation,
	})
}

// SetupWithManager wires the controller to the manager.
func (r *CellRouterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cellv1alpha1.CellRouter{}).
		Owns(&gatewayv1.Gateway{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Owns(&gatewayv1beta1.ReferenceGrant{}).
		Named("cellrouter").
		Complete(r)
}
