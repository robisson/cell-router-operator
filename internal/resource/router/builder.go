package router

import (
	"sort"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"
	"github.com/robisson/cell-router-operator/internal/utils/metadata"
)

// BackendTarget represents the Service backend information for an HTTPRoute.
type BackendTarget struct {
	Namespace string
	Name      string
	Port      int32
	Weight    *int32
}

// MutateGateway applies the desired state for the gateway.
func MutateGateway(gw *gatewayv1.Gateway, router *cellv1alpha1.CellRouter) {
	gw.Labels = metadata.Merge(gw.Labels, map[string]string{
		constants.ManagedByLabel:  constants.OperatorName,
		constants.RouterNameLabel: router.Name,
	})
	gw.Annotations = metadata.Merge(gw.Annotations, router.Spec.Gateway.Annotations)
	gw.Labels = metadata.Merge(gw.Labels, router.Spec.Gateway.Labels,
		constants.ManagedByLabel,
		constants.RouterNameLabel,
	)

	gw.Spec.GatewayClassName = gatewayv1.ObjectName(router.Spec.Gateway.GatewayClassName)

	listeners := make([]gatewayv1.Listener, 0, len(router.Spec.Gateway.Listeners))
	for _, l := range router.Spec.Gateway.Listeners {
		listener := gatewayv1.Listener{
			Name:     gatewayv1.SectionName(l.Name),
			Port:     gatewayv1.PortNumber(l.Port),
			Protocol: l.Protocol,
			Hostname: l.Hostname,
			TLS:      l.TLS,
		}
		listeners = append(listeners, listener)
	}

	sort.SliceStable(listeners, func(i, j int) bool {
		return listeners[i].Name < listeners[j].Name
	})

	gw.Spec.Listeners = listeners
}

// MutateHTTPRoute applies the desired state for an HTTPRoute resource.
func MutateHTTPRoute(route *gatewayv1.HTTPRoute, router *cellv1alpha1.CellRouter, spec cellv1alpha1.CellRouteSpec, gatewayNamespace string, backend BackendTarget) {
	route.Labels = metadata.Merge(route.Labels, map[string]string{
		constants.ManagedByLabel:  constants.OperatorName,
		constants.RouterNameLabel: router.Name,
		constants.CellNameLabel:   spec.CellRef,
	})

	parentRefs := make([]gatewayv1.ParentReference, 0, len(spec.ListenerNames))
	if len(spec.ListenerNames) == 0 {
		parentRefs = append(parentRefs, gatewayv1.ParentReference{
			Name:      gatewayv1.ObjectName(router.Spec.Gateway.Name),
			Namespace: pointerTo(gatewayv1.Namespace(gatewayNamespace)),
		})
	} else {
		for _, listener := range spec.ListenerNames {
			l := listener
			parentRefs = append(parentRefs, gatewayv1.ParentReference{
				Name:        gatewayv1.ObjectName(router.Spec.Gateway.Name),
				Namespace:   pointerTo(gatewayv1.Namespace(gatewayNamespace)),
				SectionName: pointerTo(gatewayv1.SectionName(l)),
			})
		}
	}

	matches := buildMatches(spec)

	backendRef := gatewayv1.BackendObjectReference{
		Name:      gatewayv1.ObjectName(backend.Name),
		Namespace: pointerTo(gatewayv1.Namespace(backend.Namespace)),
		Port:      pointerTo(gatewayv1.PortNumber(backend.Port)),
	}

	rule := gatewayv1.HTTPRouteRule{
		Matches: matches,
		BackendRefs: []gatewayv1.HTTPBackendRef{
			{
				BackendRef: gatewayv1.BackendRef{
					BackendObjectReference: backendRef,
					Weight:                 backend.Weight,
				},
			},
		},
	}

	route.Spec = gatewayv1.HTTPRouteSpec{
		CommonRouteSpec: gatewayv1.CommonRouteSpec{
			ParentRefs: parentRefs,
		},
		Hostnames: spec.Hostnames,
		Rules:     []gatewayv1.HTTPRouteRule{rule},
	}
}

func buildMatches(spec cellv1alpha1.CellRouteSpec) []gatewayv1.HTTPRouteMatch {
	matches := make([]gatewayv1.HTTPRouteMatch, 0, 1)

	match := gatewayv1.HTTPRouteMatch{}
	if spec.PathMatch != nil {
		match.Path = &gatewayv1.HTTPPathMatch{
			Type:  spec.PathMatch.Type,
			Value: pointerTo(spec.PathMatch.Value),
		}
	}

	if len(spec.HeaderMatches) > 0 {
		headers := make([]gatewayv1.HTTPHeaderMatch, 0, len(spec.HeaderMatches))
		for _, header := range spec.HeaderMatches {
			headers = append(headers, gatewayv1.HTTPHeaderMatch{
				Type:  header.Type,
				Name:  header.Name,
				Value: header.Value,
			})
		}
		match.Headers = headers
	}

	if len(spec.QueryParamMatches) > 0 {
		params := make([]gatewayv1.HTTPQueryParamMatch, 0, len(spec.QueryParamMatches))
		for _, param := range spec.QueryParamMatches {
			params = append(params, gatewayv1.HTTPQueryParamMatch{
				Type:  param.Type,
				Name:  param.Name,
				Value: param.Value,
			})
		}
		match.QueryParams = params
	}

	if match.Path != nil || len(match.Headers) > 0 || len(match.QueryParams) > 0 {
		matches = append(matches, match)
	}

	return matches
}

func pointerTo[T any](value T) *T {
	return &value
}
