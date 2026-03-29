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

const (
	// SelectorSourceHeader extracts a value from an HTTP header.
	SelectorSourceHeader CellPlacementSelectorSourceType = "Header"
	// SelectorSourceQueryParam extracts a value from an HTTP query parameter.
	SelectorSourceQueryParam CellPlacementSelectorSourceType = "QueryParam"
	// SelectorSourcePathCapture extracts a value from a named capture inside a path regex.
	SelectorSourcePathCapture CellPlacementSelectorSourceType = "PathCapture"
)

const (
	// SelectorOperatorExact matches one exact value.
	SelectorOperatorExact CellPlacementSelectorOperator = "Exact"
	// SelectorOperatorPrefix matches values with the configured prefix.
	SelectorOperatorPrefix CellPlacementSelectorOperator = "Prefix"
	// SelectorOperatorSuffix matches values with the configured suffix.
	SelectorOperatorSuffix CellPlacementSelectorOperator = "Suffix"
	// SelectorOperatorRange matches unsigned decimal values inside an inclusive numeric range.
	SelectorOperatorRange CellPlacementSelectorOperator = "Range"
)

// CellPlacementSelectorSourceType declares where the placement extracts a value from.
// +kubebuilder:validation:Enum=Header;QueryParam;PathCapture
type CellPlacementSelectorSourceType string

// CellPlacementSelectorOperator describes how an extracted value should be matched.
// +kubebuilder:validation:Enum=Exact;Prefix;Suffix;Range
type CellPlacementSelectorOperator string

// CellPlacementSelectorSource identifies the request field used by a selector.
type CellPlacementSelectorSource struct {
	// Type identifies whether the selector reads from a header, query parameter, or path capture.
	Type CellPlacementSelectorSourceType `json:"type"`

	// Name is the header, query parameter, or named path capture to inspect.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// PathRegex is required only for PathCapture selectors.
	// It must contain the named capture referenced by Name.
	// +optional
	PathRegex string `json:"pathRegex,omitempty"`
}

// CellPlacementNumericRange represents an inclusive unsigned decimal range.
type CellPlacementNumericRange struct {
	// Start is the inclusive range start.
	// +kubebuilder:validation:Pattern=`^(0|[1-9][0-9]*)$`
	Start string `json:"start"`

	// End is the inclusive range end.
	// +kubebuilder:validation:Pattern=`^(0|[1-9][0-9]*)$`
	End string `json:"end"`
}

// CellPlacementSelector describes one selector used to decide whether a placement applies.
// +kubebuilder:validation:XValidation:message="value is required for Exact, Prefix, and Suffix operators",rule="self.operator == 'Range' || has(self.value)"
// +kubebuilder:validation:XValidation:message="range is required for the Range operator",rule="self.operator != 'Range' || has(self.range)"
// +kubebuilder:validation:XValidation:message="range must not be set for Exact, Prefix, or Suffix operators",rule="self.operator == 'Range' || !has(self.range)"
type CellPlacementSelector struct {
	// Source defines where the value should be extracted from.
	Source CellPlacementSelectorSource `json:"source"`

	// Operator selects the matching strategy.
	Operator CellPlacementSelectorOperator `json:"operator"`

	// Value is used by Exact, Prefix, and Suffix operators.
	// +optional
	Value string `json:"value,omitempty"`

	// Range is used by the Range operator.
	// +optional
	Range *CellPlacementNumericRange `json:"range,omitempty"`
}

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

	// Selectors are combined with AND semantics.
	// Every selector must match for the placement to apply.
	// +kubebuilder:validation:MinItems=1
	Selectors []CellPlacementSelector `json:"selectors"`

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
