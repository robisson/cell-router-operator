package controller

import (
	"context"
	"fmt"
	"strings"
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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	"k8s.io/client-go/tools/record"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"
	routerresource "github.com/robisson/cell-router-operator/internal/resource/router"
)

const (
	// requeueDelay is used while waiting for Gateway API resources to report readiness.
	requeueDelay = 10 * time.Second
)

type routePlan struct {
	name         string
	spec         cellv1alpha1.CellRouteSpec
	placement    *cellv1alpha1.CellPlacement
	placementRef string
}

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
// +kubebuilder:rbac:groups=cell.cellrouter.io,resources=cellplacements,verbs=get;list;watch
// +kubebuilder:rbac:groups=cell.cellrouter.io,resources=cellplacements/status,verbs=get;update;patch
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

// reconcileNormal drives the full router lifecycle across static routes and placement-derived routes.
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

	placements, err := r.listPlacements(ctx, router.Name)
	if err != nil {
		logger.Error(err, "failed to list placements")
		r.setRouterCondition(router, cellv1alpha1.CellRouterRoutesReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, err.Error())
		r.setRouterCondition(router, cellv1alpha1.CellRouterReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, "placement discovery failed")
		_ = r.patchRouterStatus(ctx, router, statusBase)
		return ctrl.Result{}, err
	}

	routesReady, needsRequeue, managedRoutes, err := r.reconcileRoutes(ctx, router, gatewayNamespace, placements, logger)
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
		r.setRouterCondition(router, cellv1alpha1.CellRouterRoutesReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonProgressing, "waiting for all routes to become traffic-ready")
		r.setRouterCondition(router, cellv1alpha1.CellRouterReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonProgressing, "router is waiting for all routes to become traffic-ready")
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

// reconcileDeletion tears down resources in dependency order so routes and grants disappear first.
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

func (r *CellRouterReconciler) reconcileRoutes(ctx context.Context, router *cellv1alpha1.CellRouter, gatewayNamespace string, placements []cellv1alpha1.CellPlacement, logger logr.Logger) (allReady bool, needsRequeue bool, statuses []cellv1alpha1.ManagedRouteStatus, err error) {
	plans, err := buildRoutePlans(router, placements)
	if err != nil {
		return false, false, nil, err
	}

	expectedRoutes := make(map[string]struct{}, len(plans))
	expectedGrants := map[string]struct{}{}
	statuses = make([]cellv1alpha1.ManagedRouteStatus, 0, len(plans))
	allReady = true

	for _, plan := range plans {
		backends, backendRefs, reason, routeNeedsRequeue, resolveErr := r.resolveRouteBackends(ctx, plan)
		if resolveErr != nil {
			return false, true, statuses, resolveErr
		}

		routeStatus := cellv1alpha1.ManagedRouteStatus{
			Name:          plan.name,
			ListenerNames: plan.spec.ListenerNames,
			CellRef:       plan.spec.CellRef,
			BackendRefs:   backendRefs,
			PlacementRef:  plan.placementRef,
			Reason:        reason,
		}

		if plan.placement != nil {
			if err := r.patchPlacementReadiness(ctx, plan.placement, backendRefs, reason, len(backends) > 0); err != nil {
				return false, true, statuses, err
			}
		}

		if len(backends) == 0 {
			allReady = false
			needsRequeue = needsRequeue || routeNeedsRequeue
			statuses = append(statuses, routeStatus)
			logger.Info("route has no traffic-ready backends", "route", plan.name, "reason", reason)
			continue
		}

		expectedRoutes[plan.name] = struct{}{}
		for _, backend := range backends {
			expectedGrants[routerresource.ReferenceGrantName(plan.name, backend)] = struct{}{}
			if err := r.reconcileReferenceGrant(ctx, router, plan.name, gatewayNamespace, backend); err != nil {
				return false, true, statuses, err
			}
		}

		httpRoute := &gatewayv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{
			Name:      plan.name,
			Namespace: gatewayNamespace,
		}}

		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, httpRoute, func() error {
			routerresource.MutateHTTPRoute(httpRoute, router, plan.spec, gatewayNamespace, backends, plan.placementRef)
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
		if readyTime != nil {
			copy := *readyTime
			routeStatus.LastTransitionTime = &copy
		}
		statuses = append(statuses, routeStatus)
	}

	if err := r.cleanupStaleRoutes(ctx, router, gatewayNamespace, expectedRoutes); err != nil {
		return false, true, statuses, err
	}

	if err := r.cleanupStaleReferenceGrants(ctx, router, expectedGrants); err != nil {
		return false, true, statuses, err
	}

	return allReady, needsRequeue, statuses, nil
}

func buildRoutePlans(router *cellv1alpha1.CellRouter, placements []cellv1alpha1.CellPlacement) ([]routePlan, error) {
	plans := make([]routePlan, 0, len(router.Spec.Routes)+len(placements))
	seenNames := map[string]struct{}{}

	for _, routeSpec := range router.Spec.Routes {
		if _, exists := seenNames[routeSpec.Name]; exists {
			return nil, fmt.Errorf("duplicate route name %q", routeSpec.Name)
		}
		seenNames[routeSpec.Name] = struct{}{}
		plans = append(plans, routePlan{name: routeSpec.Name, spec: routeSpec})
	}

	for idx := range placements {
		placement := placements[idx]
		if _, exists := seenNames[placement.Name]; exists {
			return nil, fmt.Errorf("placement %q conflicts with an existing route name", placement.Name)
		}
		spec := routeSpecFromPlacement(&placement)
		seenNames[placement.Name] = struct{}{}
		plans = append(plans, routePlan{
			name:         placement.Name,
			spec:         spec,
			placement:    &placement,
			placementRef: placement.Name,
		})
	}

	return plans, nil
}

func routeSpecFromPlacement(placement *cellv1alpha1.CellPlacement) cellv1alpha1.CellRouteSpec {
	spec := cellv1alpha1.CellRouteSpec{
		Name:              placement.Name,
		ListenerNames:     placement.Spec.ListenerNames,
		Hostnames:         placement.Spec.Hostnames,
		PathMatch:         placement.Spec.PathMatch,
		HeaderMatches:     placement.Spec.HeaderMatches,
		QueryParamMatches: placement.Spec.QueryParamMatches,
	}

	if len(placement.Spec.Destinations) > 0 {
		spec.CellRef = placement.Spec.Destinations[0].CellRef
		spec.Weight = placement.Spec.Destinations[0].Weight
		if len(placement.Spec.Destinations) > 1 {
			spec.AdditionalBackends = append([]cellv1alpha1.CellRouteBackendRef(nil), placement.Spec.Destinations[1:]...)
		}
	}

	return spec
}

func (r *CellRouterReconciler) listPlacements(ctx context.Context, routerName string) ([]cellv1alpha1.CellPlacement, error) {
	var list cellv1alpha1.CellPlacementList
	if err := r.List(ctx, &list); err != nil {
		return nil, err
	}

	placements := make([]cellv1alpha1.CellPlacement, 0, len(list.Items))
	for idx := range list.Items {
		placement := list.Items[idx]
		if placement.Spec.RouterRef == routerName {
			placements = append(placements, placement)
		}
	}

	return placements, nil
}

func (r *CellRouterReconciler) resolveRouteBackends(ctx context.Context, plan routePlan) ([]routerresource.BackendTarget, []string, string, bool, error) {
	candidates := make([]cellv1alpha1.CellRouteBackendRef, 0, 1+len(plan.spec.AdditionalBackends))
	if plan.spec.CellRef != "" {
		candidates = append(candidates, cellv1alpha1.CellRouteBackendRef{
			CellRef: plan.spec.CellRef,
			Weight:  plan.spec.Weight,
		})
	}
	candidates = append(candidates, plan.spec.AdditionalBackends...)

	backends, backendRefs, issues, err := r.resolveBackendCandidates(ctx, candidates)
	if err != nil {
		return nil, nil, "", true, err
	}
	if len(backends) > 0 {
		reason := "traffic-ready"
		if len(issues) > 0 {
			reason = "degraded: " + strings.Join(issues, "; ")
		}
		return backends, backendRefs, reason, false, nil
	}

	if plan.spec.FallbackBackend != nil {
		fallbackBackends, fallbackRefs, fallbackIssues, err := r.resolveBackendCandidates(ctx, []cellv1alpha1.CellRouteBackendRef{*plan.spec.FallbackBackend})
		if err != nil {
			return nil, nil, "", true, err
		}
		if len(fallbackBackends) > 0 {
			return fallbackBackends, fallbackRefs, "using fallback backend", false, nil
		}
		issues = append(issues, fallbackIssues...)
	}

	reason := "waiting for traffic-ready backends"
	if len(issues) > 0 {
		reason = strings.Join(issues, "; ")
	}
	return nil, nil, reason, true, nil
}

func (r *CellRouterReconciler) resolveBackendCandidates(ctx context.Context, refs []cellv1alpha1.CellRouteBackendRef) ([]routerresource.BackendTarget, []string, []string, error) {
	backends := make([]routerresource.BackendTarget, 0, len(refs))
	backendRefs := make([]string, 0, len(refs))
	issues := make([]string, 0)

	for _, ref := range refs {
		cell := &cellv1alpha1.Cell{}
		if err := r.Get(ctx, types.NamespacedName{Name: ref.CellRef}, cell); err != nil {
			if apierrors.IsNotFound(err) {
				issues = append(issues, fmt.Sprintf("cell %q not found", ref.CellRef))
				continue
			}
			return nil, nil, nil, err
		}

		if ready, reason := isCellTrafficReady(cell); !ready {
			issues = append(issues, fmt.Sprintf("cell %q is not traffic-ready: %s", ref.CellRef, reason))
			continue
		}

		backend, err := r.resolveBackend(cell)
		if err != nil {
			issues = append(issues, fmt.Sprintf("failed to resolve backend for cell %q: %v", ref.CellRef, err))
			continue
		}
		backend.Weight = ref.Weight
		backend.CellRef = ref.CellRef
		backends = append(backends, backend)
		backendRefs = append(backendRefs, ref.CellRef)
	}

	return backends, backendRefs, issues, nil
}

func isCellTrafficReady(cell *cellv1alpha1.Cell) (bool, string) {
	switch effectiveCellState(cell) {
	case cellv1alpha1.CellStateDisabled:
		return false, "disabled"
	case cellv1alpha1.CellStateDraining:
		return false, "draining"
	}

	backendCondition := apimeta.FindStatusCondition(cell.Status.Conditions, cellv1alpha1.CellBackendReadyCondition)
	if backendCondition == nil || backendCondition.Status != metav1.ConditionTrue {
		if backendCondition != nil && backendCondition.Message != "" {
			return false, backendCondition.Message
		}
		return false, "backend condition is not ready"
	}

	readyCondition := apimeta.FindStatusCondition(cell.Status.Conditions, cellv1alpha1.CellReadyCondition)
	if readyCondition == nil || readyCondition.Status != metav1.ConditionTrue {
		if readyCondition != nil && readyCondition.Message != "" {
			return false, readyCondition.Message
		}
		return false, "cell condition is not ready"
	}

	return true, "ready"
}

// isRouteReady relies on Accepted and, when exposed by the implementation, ResolvedRefs.
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

func (r *CellRouterReconciler) reconcileReferenceGrant(ctx context.Context, router *cellv1alpha1.CellRouter, routeName, gatewayNamespace string, backend routerresource.BackendTarget) error {
	grant := &gatewayv1beta1.ReferenceGrant{ObjectMeta: metav1.ObjectMeta{
		Name:      routerresource.ReferenceGrantName(routeName, backend),
		Namespace: backend.Namespace,
	}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, grant, func() error {
		routerresource.MutateReferenceGrant(grant, router, routeName, backend.CellRef, gatewayNamespace, backend)
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

func (r *CellRouterReconciler) patchPlacementReadiness(ctx context.Context, placement *cellv1alpha1.CellPlacement, resolvedBackends []string, reason string, ready bool) error {
	base := placement.DeepCopy()
	placement.Status.ObservedGeneration = placement.Generation
	placement.Status.ResolvedBackends = resolvedBackends

	conditionStatus := metav1.ConditionFalse
	conditionReason := cellv1alpha1.ConditionReasonProgressing
	if ready {
		conditionStatus = metav1.ConditionTrue
		conditionReason = cellv1alpha1.ConditionReasonReconciled
	}

	apimeta.SetStatusCondition(&placement.Status.Conditions, metav1.Condition{
		Type:               cellv1alpha1.CellPlacementReadyCondition,
		Status:             conditionStatus,
		Reason:             conditionReason,
		Message:            reason,
		ObservedGeneration: placement.Generation,
	})

	return r.Status().Patch(ctx, placement, client.MergeFrom(base))
}

func (r *CellRouterReconciler) requestsForCell(ctx context.Context, _ client.Object) []reconcile.Request {
	var routers cellv1alpha1.CellRouterList
	if err := r.List(ctx, &routers); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(routers.Items))
	for idx := range routers.Items {
		router := routers.Items[idx]
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: router.Name}})
	}
	return requests
}

func (r *CellRouterReconciler) requestsForPlacement(_ context.Context, obj client.Object) []reconcile.Request {
	placement, ok := obj.(*cellv1alpha1.CellPlacement)
	if !ok || placement.Spec.RouterRef == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: placement.Spec.RouterRef}}}
}

// SetupWithManager wires the controller to the manager.
func (r *CellRouterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cellv1alpha1.CellRouter{}).
		Owns(&gatewayv1.Gateway{}).
		Owns(&gatewayv1.HTTPRoute{}).
		Owns(&gatewayv1beta1.ReferenceGrant{}).
		Watches(&cellv1alpha1.Cell{}, handler.EnqueueRequestsFromMapFunc(r.requestsForCell)).
		Watches(&cellv1alpha1.CellPlacement{}, handler.EnqueueRequestsFromMapFunc(r.requestsForPlacement)).
		Named("cellrouter").
		Complete(r)
}
