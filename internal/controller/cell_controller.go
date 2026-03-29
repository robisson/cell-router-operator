package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
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

	"k8s.io/client-go/tools/record"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"
	cellresource "github.com/robisson/cell-router-operator/internal/resource/cell"
)

const cellBackendRequeueDelay = 5 * time.Second

// CellReconciler reconciles a Cell object.
type CellReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=cell.cellrouter.io,resources=cells,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cell.cellrouter.io,resources=cells/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cell.cellrouter.io,resources=cells/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=limitranges,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile adjusts the cluster state so that it matches the desired Cell specification.
func (r *CellReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("cell", req.NamespacedName.Name)

	var cell cellv1alpha1.Cell
	if err := r.Get(ctx, req.NamespacedName, &cell); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !cell.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, &cell, logger)
	}

	if err := r.ensureFinalizer(ctx, &cell); err != nil {
		logger.Error(err, "failed to ensure finalizer")
		return ctrl.Result{}, err
	}

	return r.reconcileNormal(ctx, &cell, logger)
}

// reconcileNormal reconciles namespace, service, optional policies, and backend readiness.
func (r *CellReconciler) reconcileNormal(ctx context.Context, cell *cellv1alpha1.Cell, logger logr.Logger) (ctrl.Result, error) {
	statusBase := cell.DeepCopy()
	namespaceName := effectiveNamespace(cell)
	operationalState := effectiveCellState(cell)

	if err := r.reconcileNamespace(ctx, cell, namespaceName); err != nil {
		logger.Error(err, "failed to reconcile namespace", "namespace", namespaceName)
		r.setCellCondition(cell, cellv1alpha1.CellNamespaceReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, err.Error())
		r.setCellCondition(cell, cellv1alpha1.CellReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, "namespace reconciliation failed")
		_ = r.patchStatus(ctx, cell, statusBase)
		return ctrl.Result{}, err
	}
	r.setCellCondition(cell, cellv1alpha1.CellNamespaceReadyCondition, metav1.ConditionTrue, cellv1alpha1.ConditionReasonReconciled, "namespace reconciled")

	serviceFullName, err := r.reconcileService(ctx, cell, namespaceName)
	if err != nil {
		logger.Error(err, "failed to reconcile service", "service", cell.Spec.Entrypoint.ServiceName)
		r.setCellCondition(cell, cellv1alpha1.CellServiceReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, err.Error())
		r.setCellCondition(cell, cellv1alpha1.CellReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, "service reconciliation failed")
		_ = r.patchStatus(ctx, cell, statusBase)
		return ctrl.Result{}, err
	}
	r.setCellCondition(cell, cellv1alpha1.CellServiceReadyCondition, metav1.ConditionTrue, cellv1alpha1.ConditionReasonReconciled, "service reconciled")

	if err := r.reconcilePolicies(ctx, cell, namespaceName); err != nil {
		logger.Error(err, "failed to reconcile policies", "namespace", namespaceName)
		r.setCellCondition(cell, cellv1alpha1.CellPoliciesReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, err.Error())
		r.setCellCondition(cell, cellv1alpha1.CellReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, "policy reconciliation failed")
		_ = r.patchStatus(ctx, cell, statusBase)
		return ctrl.Result{}, err
	}
	r.setCellCondition(cell, cellv1alpha1.CellPoliciesReadyCondition, metav1.ConditionTrue, cellv1alpha1.ConditionReasonReconciled, "policies reconciled")

	availableEndpoints, err := r.countReadyEndpoints(ctx, namespaceName, cell.Spec.Entrypoint.ServiceName)
	if err != nil {
		logger.Error(err, "failed to resolve backend endpoints", "service", serviceFullName)
		r.setCellCondition(cell, cellv1alpha1.CellBackendReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, err.Error())
		r.setCellCondition(cell, cellv1alpha1.CellReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonError, "backend discovery failed")
		_ = r.patchStatus(ctx, cell, statusBase)
		return ctrl.Result{}, err
	}

	cell.Status.Namespace = namespaceName
	cell.Status.EntrypointService = serviceFullName
	cell.Status.ObservedGeneration = cell.Generation
	cell.Status.OperationalState = operationalState
	cell.Status.AvailableEndpoints = availableEndpoints

	needsBackendRequeue := false
	switch operationalState {
	case cellv1alpha1.CellStateDisabled:
		r.setCellCondition(cell, cellv1alpha1.CellBackendReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonLifecycleBlocked, "cell is disabled")
		r.setCellCondition(cell, cellv1alpha1.CellReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonLifecycleBlocked, "cell is disabled")
	case cellv1alpha1.CellStateDraining:
		if availableEndpoints > 0 {
			r.setCellCondition(cell, cellv1alpha1.CellBackendReadyCondition, metav1.ConditionTrue, cellv1alpha1.ConditionReasonReconciled, "backend remains available while draining")
		} else {
			r.setCellCondition(cell, cellv1alpha1.CellBackendReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonWaitingForBackend, "waiting for ready backend endpoints")
			needsBackendRequeue = true
		}
		r.setCellCondition(cell, cellv1alpha1.CellReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonLifecycleBlocked, "cell is draining and withheld from new traffic")
	default:
		if availableEndpoints == 0 {
			r.setCellCondition(cell, cellv1alpha1.CellBackendReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonWaitingForBackend, "waiting for ready backend endpoints")
			r.setCellCondition(cell, cellv1alpha1.CellReadyCondition, metav1.ConditionFalse, cellv1alpha1.ConditionReasonWaitingForBackend, "cell is waiting for ready backend endpoints")
			needsBackendRequeue = true
		} else {
			r.setCellCondition(cell, cellv1alpha1.CellBackendReadyCondition, metav1.ConditionTrue, cellv1alpha1.ConditionReasonReconciled, "backend endpoints are ready")
			r.setCellCondition(cell, cellv1alpha1.CellReadyCondition, metav1.ConditionTrue, cellv1alpha1.ConditionReasonReconciled, "cell is ready")
		}
	}

	if err := r.patchStatus(ctx, cell, statusBase); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	if needsBackendRequeue {
		return ctrl.Result{RequeueAfter: cellBackendRequeueDelay}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileDeletion removes only resources this controller can prove it owns,
// which protects shared namespaces and preexisting Services from accidental deletion.
func (r *CellReconciler) reconcileDeletion(ctx context.Context, cell *cellv1alpha1.Cell, logger logr.Logger) (ctrl.Result, error) {
	namespaceName := effectiveNamespace(cell)

	if err := r.deleteEntrypointService(ctx, cell, namespaceName); err != nil {
		logger.Error(err, "failed to delete entrypoint service during finalization")
		return ctrl.Result{}, err
	}

	if cell.Spec.TearDownOnDelete {
		if err := r.deleteNamespaceIfManaged(ctx, cell, namespaceName); err != nil {
			logger.Error(err, "failed to delete namespace during finalization", "namespace", namespaceName)
			return ctrl.Result{}, err
		}
	}

	if controllerutil.ContainsFinalizer(cell, constants.FinalizerCell) {
		patch := client.MergeFrom(cell.DeepCopy())
		controllerutil.RemoveFinalizer(cell, constants.FinalizerCell)
		if err := r.Patch(ctx, cell, patch); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// ensureFinalizer makes sure the Cell finalizer is present before reconciliation continues.
func (r *CellReconciler) ensureFinalizer(ctx context.Context, cell *cellv1alpha1.Cell) error {
	if controllerutil.ContainsFinalizer(cell, constants.FinalizerCell) {
		return nil
	}

	patch := client.MergeFrom(cell.DeepCopy())
	controllerutil.AddFinalizer(cell, constants.FinalizerCell)
	return r.Patch(ctx, cell, patch)
}

// reconcileNamespace creates or updates the namespace managed for the Cell.
func (r *CellReconciler) reconcileNamespace(ctx context.Context, cell *cellv1alpha1.Cell, name string) error {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, namespace, func() error {
		cellresource.MutateNamespace(namespace, cell)
		return nil
	})
	if err == nil && r.Recorder != nil {
		r.Recorder.Eventf(cell, corev1.EventTypeNormal, "NamespaceReconciled", "Reconciled namespace %s", name)
	}
	return err
}

// reconcileService creates or updates the Cell entrypoint Service.
func (r *CellReconciler) reconcileService(ctx context.Context, cell *cellv1alpha1.Cell, namespace string) (string, error) {
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
		Name:      cell.Spec.Entrypoint.ServiceName,
		Namespace: namespace,
	}}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, service, func() error {
		cellresource.MutateService(service, cell)
		return controllerutil.SetControllerReference(cell, service, r.Scheme)
	})
	if err != nil {
		return "", err
	}

	if r.Recorder != nil {
		r.Recorder.Eventf(cell, corev1.EventTypeNormal, "ServiceReconciled", "Reconciled service %s/%s", namespace, service.Name)
	}

	return fmt.Sprintf("%s/%s", namespace, service.Name), nil
}

// reconcilePolicies creates or removes optional policy resources based on the Cell spec.
func (r *CellReconciler) reconcilePolicies(ctx context.Context, cell *cellv1alpha1.Cell, namespace string) error {
	if err := r.reconcileResourceQuota(ctx, cell, namespace); err != nil {
		return err
	}
	if err := r.reconcileLimitRange(ctx, cell, namespace); err != nil {
		return err
	}
	if err := r.reconcileNetworkPolicy(ctx, cell, namespace); err != nil {
		return err
	}
	return nil
}

func (r *CellReconciler) reconcileResourceQuota(ctx context.Context, cell *cellv1alpha1.Cell, namespace string) error {
	if cell.Spec.Policies == nil || cell.Spec.Policies.ResourceQuota == nil {
		return r.deleteManagedResourceQuota(ctx, cell, namespace)
	}

	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: constants.CellResourceQuotaName, Namespace: namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, quota, func() error {
		cellresource.MutateResourceQuota(quota, cell)
		return controllerutil.SetControllerReference(cell, quota, r.Scheme)
	})
	return err
}

func (r *CellReconciler) reconcileLimitRange(ctx context.Context, cell *cellv1alpha1.Cell, namespace string) error {
	if cell.Spec.Policies == nil || cell.Spec.Policies.LimitRange == nil {
		return r.deleteManagedLimitRange(ctx, cell, namespace)
	}

	limitRange := &corev1.LimitRange{ObjectMeta: metav1.ObjectMeta{Name: constants.CellLimitRangeName, Namespace: namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, limitRange, func() error {
		cellresource.MutateLimitRange(limitRange, cell)
		return controllerutil.SetControllerReference(cell, limitRange, r.Scheme)
	})
	return err
}

func (r *CellReconciler) reconcileNetworkPolicy(ctx context.Context, cell *cellv1alpha1.Cell, namespace string) error {
	if cell.Spec.Policies == nil || cell.Spec.Policies.NetworkPolicy == nil || !cell.Spec.Policies.NetworkPolicy.Enabled {
		return r.deleteManagedNetworkPolicy(ctx, cell, namespace)
	}

	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: constants.CellNetworkPolicyName, Namespace: namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, policy, func() error {
		cellresource.MutateNetworkPolicy(policy, cell)
		return controllerutil.SetControllerReference(cell, policy, r.Scheme)
	})
	return err
}

func (r *CellReconciler) countReadyEndpoints(ctx context.Context, namespace, serviceName string) (int32, error) {
	var slices discoveryv1.EndpointSliceList
	if err := r.List(ctx, &slices, client.InNamespace(namespace), client.MatchingLabels{discoveryv1.LabelServiceName: serviceName}); err != nil {
		return 0, err
	}

	var count int32
	for idx := range slices.Items {
		slice := slices.Items[idx]
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
				continue
			}
			count += int32(len(endpoint.Addresses))
		}
	}

	return count, nil
}

// deleteEntrypointService removes the Service only when the Cell owns it.
func (r *CellReconciler) deleteEntrypointService(ctx context.Context, cell *cellv1alpha1.Cell, namespace string) error {
	if namespace == "" {
		return nil
	}

	service := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: cell.Spec.Entrypoint.ServiceName}, service)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if !metav1.IsControlledBy(service, cell) {
		return nil
	}

	return client.IgnoreNotFound(r.Delete(ctx, service))
}

func (r *CellReconciler) deleteManagedResourceQuota(ctx context.Context, cell *cellv1alpha1.Cell, namespace string) error {
	quota := &corev1.ResourceQuota{}
	err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: constants.CellResourceQuotaName}, quota)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !metav1.IsControlledBy(quota, cell) {
		return nil
	}
	return client.IgnoreNotFound(r.Delete(ctx, quota))
}

func (r *CellReconciler) deleteManagedLimitRange(ctx context.Context, cell *cellv1alpha1.Cell, namespace string) error {
	limitRange := &corev1.LimitRange{}
	err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: constants.CellLimitRangeName}, limitRange)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !metav1.IsControlledBy(limitRange, cell) {
		return nil
	}
	return client.IgnoreNotFound(r.Delete(ctx, limitRange))
}

func (r *CellReconciler) deleteManagedNetworkPolicy(ctx context.Context, cell *cellv1alpha1.Cell, namespace string) error {
	policy := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: constants.CellNetworkPolicyName}, policy)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !metav1.IsControlledBy(policy, cell) {
		return nil
	}
	return client.IgnoreNotFound(r.Delete(ctx, policy))
}

// deleteNamespaceIfManaged removes the namespace only when the operator labels indicate it created it.
func (r *CellReconciler) deleteNamespaceIfManaged(ctx context.Context, cell *cellv1alpha1.Cell, namespace string) error {
	if namespace == "" {
		return nil
	}

	ns := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{Name: namespace}, ns)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if ns.Labels[constants.ManagedByLabel] != constants.OperatorName || ns.Labels[constants.CellNameLabel] != cell.Name {
		return nil
	}

	return client.IgnoreNotFound(r.Delete(ctx, ns))
}

// patchStatus uses a merge patch so condition updates do not wipe unrelated status fields.
func (r *CellReconciler) patchStatus(ctx context.Context, cell *cellv1alpha1.Cell, base *cellv1alpha1.Cell) error {
	return r.Status().Patch(ctx, cell, client.MergeFrom(base))
}

// setCellCondition upserts a status condition on the Cell.
func (r *CellReconciler) setCellCondition(cell *cellv1alpha1.Cell, condType string, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&cell.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: cell.Generation,
	})
}

// effectiveNamespace centralizes defaulting so reconciliation, status, and finalization agree.
func effectiveNamespace(cell *cellv1alpha1.Cell) string {
	if cell.Spec.Namespace != "" {
		return cell.Spec.Namespace
	}
	return cell.Name
}

func effectiveCellState(cell *cellv1alpha1.Cell) cellv1alpha1.CellState {
	if cell.Spec.State == "" {
		return cellv1alpha1.CellStateActive
	}
	return cell.Spec.State
}

func (r *CellReconciler) requestsForEndpointSlice(ctx context.Context, obj client.Object) []reconcile.Request {
	slice, ok := obj.(*discoveryv1.EndpointSlice)
	if !ok {
		return nil
	}

	serviceName := slice.Labels[discoveryv1.LabelServiceName]
	if serviceName == "" {
		return nil
	}

	var cells cellv1alpha1.CellList
	if err := r.List(ctx, &cells); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0, len(cells.Items))
	for idx := range cells.Items {
		cell := cells.Items[idx]
		if effectiveNamespace(&cell) != slice.Namespace {
			continue
		}
		if cell.Spec.Entrypoint.ServiceName != serviceName {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: cell.Name}})
	}

	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *CellReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cellv1alpha1.Cell{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ResourceQuota{}).
		Owns(&corev1.LimitRange{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Watches(&discoveryv1.EndpointSlice{}, handler.EnqueueRequestsFromMapFunc(r.requestsForEndpointSlice)).
		Named("cell").
		Complete(r)
}
