package controller

import (
	"context"
	"fmt"

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

	"k8s.io/client-go/tools/record"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"
	cellresource "github.com/robisson/cell-router-operator/internal/resource/cell"
)

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

// reconcileNormal reconciles the namespace and entrypoint Service and then
// projects only that controller-owned state back into Cell status.
func (r *CellReconciler) reconcileNormal(ctx context.Context, cell *cellv1alpha1.Cell, logger logr.Logger) (ctrl.Result, error) {
	// Patch from a deep copy so status writes do not interfere with metadata or
	// spec changes that may have happened after this reconcile started.
	statusBase := cell.DeepCopy()
	namespaceName := effectiveNamespace(cell)

	if err := r.reconcileNamespace(ctx, cell, namespaceName); err != nil {
		logger.Error(err, "failed to reconcile namespace", "namespace", namespaceName)
		// Persist the failure reason best-effort so users can inspect status even
		// though the reconcile returns an error and will be retried.
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

	cell.Status.Namespace = namespaceName
	cell.Status.EntrypointService = serviceFullName
	cell.Status.ObservedGeneration = cell.Generation
	r.setCellCondition(cell, cellv1alpha1.CellReadyCondition, metav1.ConditionTrue, cellv1alpha1.ConditionReasonReconciled, "cell is ready")

	if err := r.patchStatus(ctx, cell, statusBase); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
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
// Labels, rather than owner references, are used to track ownership because
// namespaces are cluster-scoped.
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

// reconcileService creates or updates the Cell entrypoint Service and attaches
// an owner reference so Kubernetes GC can remove it with the Cell.
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

// deleteEntrypointService removes the Service only when the Cell owns it.
// That guard allows users to bring their own Service name without the operator
// deleting something it did not create.
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

// deleteNamespaceIfManaged removes the namespace only when the operator labels
// indicate it created that namespace specifically for this Cell.
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

// patchStatus uses a merge patch so condition updates do not wipe unrelated
// status fields already computed earlier in the reconcile.
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

// effectiveNamespace centralizes defaulting so reconciliation, status, and
// finalization all agree on where Cell-owned resources live.
func effectiveNamespace(cell *cellv1alpha1.Cell) string {
	if cell.Spec.Namespace != "" {
		return cell.Spec.Namespace
	}
	return cell.Name
}

// SetupWithManager sets up the controller with the Manager.
func (r *CellReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cellv1alpha1.Cell{}).
		Owns(&corev1.Service{}).
		Named("cell").
		Complete(r)
}
