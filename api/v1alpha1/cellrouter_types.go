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
	// CellRouterReadyCondition indicates that the router is serving traffic as configured.
	CellRouterReadyCondition = "Ready"
	// CellRouterGatewayReadyCondition tracks the readiness of the managed Gateway resource.
	CellRouterGatewayReadyCondition = "GatewayReady"
	// CellRouterRoutesReadyCondition tracks the readiness of the managed HTTPRoutes.
	CellRouterRoutesReadyCondition = "RoutesReady"
)

// CellRouterSpec defines the desired state of CellRouter.
type CellRouterSpec struct {
	// Gateway defines the Gateway resource that exposes the cell entrypoints.
	// +kubebuilder:validation:Required
	Gateway CellGatewaySpec `json:"gateway"`

	// Routes describes how traffic should be routed to cell entrypoints.
	// +kubebuilder:validation:MinItems=1
	Routes []CellRouteSpec `json:"routes"`
}

// CellGatewaySpec specifies the gateway that fronts the cells.
type CellGatewaySpec struct {
	// Name is the name of the managed Gateway.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace is the namespace where the Gateway is created.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Namespace string `json:"namespace"`

	// GatewayClassName references the GatewayClass to use.
	// +kubebuilder:validation:MinLength=1
	GatewayClassName string `json:"gatewayClassName"`

	// Labels are merged into the Gateway labels.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations are merged into the Gateway annotations.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Listeners configures the listeners exposed by the Gateway.
	// +kubebuilder:validation:MinItems=1
	Listeners []CellGatewayListener `json:"listeners"`
}

// CellGatewayListener represents a single gateway listener.
type CellGatewayListener struct {
	// Name uniquely identifies the listener within the Gateway.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Port is the network port exposed by the listener.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Protocol defines the protocol supported by the listener.
	Protocol gatewayv1.ProtocolType `json:"protocol"`

	// Hostname restricts the listener to a specific hostname when provided.
	// +optional
	Hostname *gatewayv1.Hostname `json:"hostname,omitempty"`

	// TLS configures TLS settings for the listener when the protocol requires it.
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	TLS *gatewayv1.ListenerTLSConfig `json:"tls,omitempty"`
}

// CellRouteSpec describes an HTTPRoute managed by the operator.
type CellRouteSpec struct {
	// Name is the name of the HTTPRoute resource generated for this rule.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// CellRef points to the target Cell that receives the traffic.
	// +kubebuilder:validation:MinLength=1
	CellRef string `json:"cellRef"`

	// ListenerNames restricts the route to one or more listeners. When empty, all listeners apply.
	// +optional
	ListenerNames []string `json:"listenerNames,omitempty"`

	// Hostnames declares the hostnames handled by this route.
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

	// Weight controls the relative traffic weight towards the cell.
	// +optional
	Weight *int32 `json:"weight,omitempty"`

	// AdditionalBackends expands the route into a weighted multi-backend rule.
	// Each referenced Cell must be traffic-ready before it is included.
	// +optional
	AdditionalBackends []CellRouteBackendRef `json:"additionalBackends,omitempty"`

	// FallbackBackend is used when no primary backend for the route is traffic-ready.
	// +optional
	FallbackBackend *CellRouteBackendRef `json:"fallbackBackend,omitempty"`
}

// CellRouteBackendRef points to a Cell and optional weight within a generated HTTPRoute rule.
type CellRouteBackendRef struct {
	// CellRef points to the target Cell.
	// +kubebuilder:validation:MinLength=1
	CellRef string `json:"cellRef"`

	// Weight controls the relative traffic weight towards the referenced Cell.
	// +optional
	Weight *int32 `json:"weight,omitempty"`
}

// HTTPPathMatch mirrors gateway-api HTTPPathMatch with validation markers.
type HTTPPathMatch struct {
	// Type is the path matching strategy. Defaults to PathMatchPathPrefix.
	// +optional
	Type *gatewayv1.PathMatchType `json:"type,omitempty"`

	// Value is the path value.
	// +kubebuilder:validation:MinLength=1
	Value string `json:"value"`
}

// HTTPHeaderMatch represents a single HTTP header match rule.
type HTTPHeaderMatch struct {
	// Name is the HTTP header name to match.
	Name gatewayv1.HTTPHeaderName `json:"name"`

	// Value is the HTTP header value to match.
	// +kubebuilder:validation:MinLength=1
	Value string `json:"value"`

	// Type selects the header match strategy. Defaults to HeaderMatchExact.
	// +optional
	Type *gatewayv1.HeaderMatchType `json:"type,omitempty"`
}

// HTTPQueryParamMatch represents a single query parameter match rule.
type HTTPQueryParamMatch struct {
	// Name is the query parameter name.
	Name gatewayv1.HTTPHeaderName `json:"name"`

	// Value is the expected parameter value.
	// +kubebuilder:validation:MinLength=1
	Value string `json:"value"`

	// Type selects the match strategy. Defaults to QueryParamMatchExact.
	// +optional
	Type *gatewayv1.QueryParamMatchType `json:"type,omitempty"`
}

// ManagedRouteStatus reports the status of a generated HTTPRoute.
type ManagedRouteStatus struct {
	// Name identifies the HTTPRoute.
	// +optional
	Name string `json:"name,omitempty"`

	// ListenerNames lists the listeners associated with the route.
	// +optional
	ListenerNames []string `json:"listenerNames,omitempty"`

	// CellRef is the cell the route points to.
	// +optional
	CellRef string `json:"cellRef,omitempty"`

	// BackendRefs lists the backend cells currently materialized for the route.
	// +optional
	BackendRefs []string `json:"backendRefs,omitempty"`

	// PlacementRef identifies the CellPlacement that produced the route when applicable.
	// +optional
	PlacementRef string `json:"placementRef,omitempty"`

	// Reason summarizes why the route is ready or waiting.
	// +optional
	Reason string `json:"reason,omitempty"`

	// LastTransitionTime is the last time the route condition changed.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
}

// CellRouterStatus defines the observed state of CellRouter.
type CellRouterStatus struct {
	// ObservedGeneration tracks the last reconciled generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ManagedGatewayRef stores the namespace/name of the managed gateway.
	// +optional
	ManagedGatewayRef string `json:"managedGatewayRef,omitempty"`

	// ManagedRoutes contains the managed route summary.
	// +optional
	ManagedRoutes []ManagedRouteStatus `json:"managedRoutes,omitempty"`

	// Conditions represent the current state of the CellRouter resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cellrtr

// CellRouter represents the routing layer that exposes cell entrypoints.
type CellRouter struct {
	metav1.TypeMeta `json:",inline"`

	// ObjectMeta contains the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec defines the desired state of CellRouter.
	// +required
	Spec CellRouterSpec `json:"spec"`

	// Status defines the observed state of CellRouter.
	// +optional
	Status CellRouterStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// CellRouterList contains a list of CellRouter resources.
type CellRouterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CellRouter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CellRouter{}, &CellRouterList{})
}
