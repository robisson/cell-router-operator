/*
Copyright 2025.

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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// CellReadyCondition indicates that the Cell is fully reconciled.
	CellReadyCondition = "Ready"
	// CellNamespaceReadyCondition tracks the readiness of the managed Namespace.
	CellNamespaceReadyCondition = "NamespaceReady"
	// CellServiceReadyCondition tracks the readiness of the managed Service.
	CellServiceReadyCondition = "ServiceReady"
	// CellBackendReadyCondition tracks whether the entrypoint currently resolves to ready backends.
	CellBackendReadyCondition = "BackendReady"
	// CellPoliciesReadyCondition tracks the readiness of optional namespace-scoped policy resources.
	CellPoliciesReadyCondition = "PoliciesReady"

	// ConditionReasonProgressing is used while the controller is working towards the desired state.
	ConditionReasonProgressing = "Progressing"
	// ConditionReasonReconciled marks a successful reconciliation step.
	ConditionReasonReconciled = "Reconciled"
	// ConditionReasonError marks a failure during reconciliation.
	ConditionReasonError = "Error"
	// ConditionReasonWaitingForBackend marks a resource that is structurally reconciled but not traffic-ready yet.
	ConditionReasonWaitingForBackend = "WaitingForBackend"
	// ConditionReasonLifecycleBlocked marks a resource withheld from traffic because of its declared lifecycle state.
	ConditionReasonLifecycleBlocked = "LifecycleBlocked"
)

// CellState captures the operator-controlled lifecycle state of a cell.
type CellState string

const (
	// CellStateActive accepts new traffic when the entrypoint backend is healthy.
	CellStateActive CellState = "Active"
	// CellStateDraining keeps workloads running but prevents new traffic from being routed to the cell.
	CellStateDraining CellState = "Draining"
	// CellStateDisabled fully withdraws the cell from routing.
	CellStateDisabled CellState = "Disabled"
)

// CellSpec defines the desired state of Cell.
type CellSpec struct {
	// Namespace overrides the namespace name generated for the cell.
	// When omitted, the namespace defaults to the Cell's name.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Entrypoint configures the Service that exposes workloads inside the cell.
	// +kubebuilder:validation:Required
	Entrypoint CellEntrypointSpec `json:"entrypoint"`

	// WorkloadSelector selects the Pods that back the managed Service.
	// +optional
	WorkloadSelector map[string]string `json:"workloadSelector,omitempty"`

	// State declares whether the cell should accept traffic, drain, or stay disabled.
	// +kubebuilder:validation:Enum=Active;Draining;Disabled
	// +optional
	State CellState `json:"state,omitempty"`

	// NamespaceLabels are merged into the managed Namespace labels.
	// Keys managed by the operator cannot be overridden.
	// +optional
	NamespaceLabels map[string]string `json:"namespaceLabels,omitempty"`

	// NamespaceAnnotations are merged into the managed Namespace annotations.
	// Keys managed by the operator cannot be overridden.
	// +optional
	NamespaceAnnotations map[string]string `json:"namespaceAnnotations,omitempty"`

	// ServiceLabels are merged into the managed Service labels.
	// +optional
	ServiceLabels map[string]string `json:"serviceLabels,omitempty"`

	// ServiceAnnotations are merged into the managed Service annotations.
	// +optional
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// TearDownOnDelete controls whether the operator should delete the managed namespace
	// once the Cell resource is removed. The default is false to protect workloads.
	// +optional
	TearDownOnDelete bool `json:"tearDownOnDelete,omitempty"`

	// Policies configures optional namespace-scoped resources managed alongside the cell.
	// +optional
	Policies *CellPoliciesSpec `json:"policies,omitempty"`
}

// CellEntrypointSpec describes the service exposed for the cell.
type CellEntrypointSpec struct {
	// ServiceName defines the name of the Service created inside the cell namespace.
	// +kubebuilder:validation:MinLength=1
	ServiceName string `json:"serviceName"`

	// Port specifies the port exposed by the Service.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// TargetPort overrides the port targeted on the selected pods.
	// Defaults to the same value as Port when omitted.
	// +optional
	TargetPort *int32 `json:"targetPort,omitempty"`

	// Protocol defines the Service port protocol. Defaults to TCP when omitted.
	// +optional
	Protocol corev1.Protocol `json:"protocol,omitempty"`

	// Type controls the Service type. Defaults to ClusterIP when omitted.
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`
}

// CellPoliciesSpec groups optional namespace-level policy resources managed by the operator.
type CellPoliciesSpec struct {
	// ResourceQuota requests creation of a ResourceQuota in the cell namespace.
	// +optional
	ResourceQuota *CellResourceQuotaSpec `json:"resourceQuota,omitempty"`

	// LimitRange requests creation of a LimitRange for container defaults and bounds.
	// +optional
	LimitRange *CellLimitRangeSpec `json:"limitRange,omitempty"`

	// NetworkPolicy requests creation of a NetworkPolicy around the cell workloads.
	// +optional
	NetworkPolicy *CellNetworkPolicySpec `json:"networkPolicy,omitempty"`
}

// CellResourceQuotaSpec describes a namespaced ResourceQuota managed for the cell.
type CellResourceQuotaSpec struct {
	// Hard lists the enforced quota limits.
	// +optional
	Hard corev1.ResourceList `json:"hard,omitempty"`
}

// CellLimitRangeSpec describes a simple container-scoped LimitRange managed for the cell.
type CellLimitRangeSpec struct {
	// Default applies default limits to containers that omit them.
	// +optional
	Default corev1.ResourceList `json:"default,omitempty"`

	// DefaultRequest applies default requests to containers that omit them.
	// +optional
	DefaultRequest corev1.ResourceList `json:"defaultRequest,omitempty"`

	// Max defines upper bounds for container resources.
	// +optional
	Max corev1.ResourceList `json:"max,omitempty"`

	// Min defines lower bounds for container resources.
	// +optional
	Min corev1.ResourceList `json:"min,omitempty"`
}

// CellNetworkPolicySpec configures the optional ingress policy managed for the cell workloads.
type CellNetworkPolicySpec struct {
	// Enabled controls whether the operator should manage a NetworkPolicy for the cell.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// AllowedNamespaceLabels selects external namespaces that may reach the cell workloads.
	// +optional
	AllowedNamespaceLabels map[string]string `json:"allowedNamespaceLabels,omitempty"`
}

// CellStatus defines the observed state of Cell.
type CellStatus struct {
	// ObservedGeneration tracks the last reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Namespace reflects the effective namespace name managed for the cell.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// EntrypointService holds the fully qualified name (namespace/name) of the managed Service.
	// +optional
	EntrypointService string `json:"entrypointService,omitempty"`

	// OperationalState echoes the lifecycle state currently enforced by the operator.
	// +optional
	OperationalState CellState `json:"operationalState,omitempty"`

	// AvailableEndpoints reports how many ready endpoints currently back the entrypoint Service.
	// +optional
	AvailableEndpoints int32 `json:"availableEndpoints,omitempty"`

	// Conditions represent the current state of the Cell resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cell

// Cell represents a managed cell and its entrypoint service.
type Cell struct {
	metav1.TypeMeta `json:",inline"`

	// ObjectMeta contains the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec defines the desired state of Cell.
	// +required
	Spec CellSpec `json:"spec"`

	// Status defines the observed state of Cell.
	// +optional
	Status CellStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// CellList contains a list of Cell resources.
type CellList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cell `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Cell{}, &CellList{})
}
