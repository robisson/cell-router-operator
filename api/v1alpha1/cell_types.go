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

	// ConditionReasonProgressing is used while the controller is working towards the desired state.
	ConditionReasonProgressing = "Progressing"
	// ConditionReasonReconciled marks a successful reconciliation step.
	ConditionReasonReconciled = "Reconciled"
	// ConditionReasonError marks a failure during reconciliation.
	ConditionReasonError = "Error"
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
