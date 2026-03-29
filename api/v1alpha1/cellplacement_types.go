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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	// CellPlacementReadyCondition indicates that the placement resolved at least one backend.
	CellPlacementReadyCondition = "Ready"
)

// CellPlacementSpec defines a reusable placement rule that a CellRouter can materialize as an HTTPRoute.
type CellPlacementSpec struct {
	// RouterRef points to the CellRouter responsible for materializing this placement.
	// +kubebuilder:validation:MinLength=1
	RouterRef string `json:"routerRef"`

	// ListenerNames restrict the generated route to specific listeners.
	// +optional
	ListenerNames []string `json:"listenerNames,omitempty"`

	// Hostnames declares the hostnames handled by the placement route.
	// +optional
	Hostnames []gatewayv1.Hostname `json:"hostnames,omitempty"`

	// PathMatch constrains traffic to a specific path expression.
	// +optional
	PathMatch *HTTPPathMatch `json:"pathMatch,omitempty"`

	// HeaderMatches constrains traffic using HTTP header matching.
	// +optional
	HeaderMatches []HTTPHeaderMatch `json:"headerMatches,omitempty"`

	// QueryParamMatches constrains traffic using HTTP query parameter matching.
	// +optional
	QueryParamMatches []HTTPQueryParamMatch `json:"queryParamMatches,omitempty"`

	// Destinations defines the cells and weights selected by this placement.
	// +kubebuilder:validation:MinItems=1
	Destinations []CellRouteBackendRef `json:"destinations"`
}

// CellPlacementStatus describes the observed state of a placement rule.
type CellPlacementStatus struct {
	// ObservedGeneration tracks the last reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ResolvedBackends lists the currently selected traffic-ready cells.
	// +optional
	ResolvedBackends []string `json:"resolvedBackends,omitempty"`

	// Conditions represent the current state of the placement.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cellplc

// CellPlacement declares a router-scoped traffic partition that resolves to one or more cells.
type CellPlacement struct {
	metav1.TypeMeta `json:",inline"`

	// ObjectMeta contains the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec defines the desired placement behavior.
	// +required
	Spec CellPlacementSpec `json:"spec"`

	// Status defines the observed state of the placement.
	// +optional
	Status CellPlacementStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// CellPlacementList contains a list of CellPlacement resources.
type CellPlacementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CellPlacement `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CellPlacement{}, &CellPlacementList{})
}
