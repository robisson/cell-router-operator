package cell

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"
	"github.com/robisson/cell-router-operator/internal/utils/metadata"
)

const portNameTemplate = "port-%d"

// MutateNamespace applies the desired state for the cell namespace into the provided object.
func MutateNamespace(ns *corev1.Namespace, cell *cellv1alpha1.Cell) {
	// These labels are part of the operator's ownership contract and are
	// intentionally protected from user overrides.
	ns.Labels = metadata.Merge(ns.Labels, map[string]string{
		constants.ManagedByLabel: constants.OperatorName,
		constants.CellNameLabel:  cell.Name,
	})
	ns.Annotations = metadata.Merge(ns.Annotations, cell.Spec.NamespaceAnnotations)
	ns.Labels = metadata.Merge(ns.Labels, cell.Spec.NamespaceLabels, constants.ManagedByLabel, constants.CellNameLabel)
}

// MutateService applies the desired state for the cell entrypoint service into the provided object.
func MutateService(svc *corev1.Service, cell *cellv1alpha1.Cell) {
	svc.Labels = metadata.Merge(svc.Labels, map[string]string{
		constants.ManagedByLabel:         constants.OperatorName,
		constants.CellNameLabel:          cell.Name,
		constants.EntrypointServiceLabel: cell.Spec.Entrypoint.ServiceName,
	})
	svc.Annotations = metadata.Merge(svc.Annotations, cell.Spec.ServiceAnnotations)
	svc.Labels = metadata.Merge(svc.Labels, cell.Spec.ServiceLabels,
		constants.ManagedByLabel,
		constants.CellNameLabel,
		constants.EntrypointServiceLabel,
	)

	// Reset selectors on every reconcile so labels removed from spec do not
	// linger and keep targeting stale workloads.
	if svc.Spec.Selector == nil {
		svc.Spec.Selector = map[string]string{}
	} else {
		for k := range svc.Spec.Selector {
			delete(svc.Spec.Selector, k)
		}
	}

	for k, v := range WorkloadSelector(cell) {
		svc.Spec.Selector[k] = v
	}

	serviceType := cell.Spec.Entrypoint.Type
	if serviceType == "" {
		serviceType = corev1.ServiceTypeClusterIP
	}
	svc.Spec.Type = serviceType

	protocol := cell.Spec.Entrypoint.Protocol
	if protocol == "" {
		protocol = corev1.ProtocolTCP
	}

	targetPort := cell.Spec.Entrypoint.Port
	if cell.Spec.Entrypoint.TargetPort != nil {
		targetPort = *cell.Spec.Entrypoint.TargetPort
	}

	// A Cell intentionally exposes a single logical entrypoint. Rebuild the
	// ports slice instead of mutating it in place to avoid stale ports drifting.
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       fmt.Sprintf(portNameTemplate, cell.Spec.Entrypoint.Port),
			Port:       cell.Spec.Entrypoint.Port,
			TargetPort: intstr.FromInt(int(targetPort)),
			Protocol:   protocol,
		},
	}
}

// MutateResourceQuota applies the desired state for the cell ResourceQuota.
func MutateResourceQuota(quota *corev1.ResourceQuota, cell *cellv1alpha1.Cell) {
	quota.Labels = metadata.Merge(quota.Labels, map[string]string{
		constants.ManagedByLabel: constants.OperatorName,
		constants.CellNameLabel:  cell.Name,
	})
	if cell.Spec.Policies == nil || cell.Spec.Policies.ResourceQuota == nil {
		quota.Spec.Hard = nil
		return
	}
	quota.Spec.Hard = cell.Spec.Policies.ResourceQuota.Hard.DeepCopy()
}

// MutateLimitRange applies the desired state for the cell LimitRange.
func MutateLimitRange(limitRange *corev1.LimitRange, cell *cellv1alpha1.Cell) {
	limitRange.Labels = metadata.Merge(limitRange.Labels, map[string]string{
		constants.ManagedByLabel: constants.OperatorName,
		constants.CellNameLabel:  cell.Name,
	})

	item := corev1.LimitRangeItem{Type: corev1.LimitTypeContainer}
	if cell.Spec.Policies != nil && cell.Spec.Policies.LimitRange != nil {
		item.Default = cell.Spec.Policies.LimitRange.Default.DeepCopy()
		item.DefaultRequest = cell.Spec.Policies.LimitRange.DefaultRequest.DeepCopy()
		item.Max = cell.Spec.Policies.LimitRange.Max.DeepCopy()
		item.Min = cell.Spec.Policies.LimitRange.Min.DeepCopy()
	}
	limitRange.Spec.Limits = []corev1.LimitRangeItem{item}
}

// MutateNetworkPolicy applies the desired ingress policy for the cell workloads.
func MutateNetworkPolicy(policy *networkingv1.NetworkPolicy, cell *cellv1alpha1.Cell) {
	selectorLabels := WorkloadSelector(cell)

	policy.Labels = metadata.Merge(policy.Labels, map[string]string{
		constants.ManagedByLabel: constants.OperatorName,
		constants.CellNameLabel:  cell.Name,
	})
	policy.Spec.PodSelector = metav1.LabelSelector{MatchLabels: selectorLabels}
	policy.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}

	peers := []networkingv1.NetworkPolicyPeer{
		// Keep same-namespace communication available for probes and sidecars.
		{PodSelector: &metav1.LabelSelector{}},
	}
	if cell.Spec.Policies != nil && cell.Spec.Policies.NetworkPolicy != nil && len(cell.Spec.Policies.NetworkPolicy.AllowedNamespaceLabels) > 0 {
		peers = append(peers, networkingv1.NetworkPolicyPeer{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: cell.Spec.Policies.NetworkPolicy.AllowedNamespaceLabels},
		})
	}

	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{From: peers}}
}

// WorkloadSelector returns the selector applied to the managed Service and policy resources.
func WorkloadSelector(cell *cellv1alpha1.Cell) map[string]string {
	if len(cell.Spec.WorkloadSelector) > 0 {
		selector := make(map[string]string, len(cell.Spec.WorkloadSelector))
		for k, v := range cell.Spec.WorkloadSelector {
			selector[k] = v
		}
		return selector
	}

	// The default convention keeps the API lightweight: a workload labeled with
	// the cell name becomes routable without an explicit selector.
	return map[string]string{constants.CellNameLabel: cell.Name}
}
