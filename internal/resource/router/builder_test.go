package router

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	"github.com/robisson/cell-router-operator/internal/constants"
)

func TestMutateGateway(t *testing.T) {
	router := &cellv1alpha1.CellRouter{ObjectMeta: metav1.ObjectMeta{Name: "global"},
		Spec: cellv1alpha1.CellRouterSpec{Gateway: cellv1alpha1.CellGatewaySpec{
			Name:             "ingress",
			Namespace:        "cell-routing",
			GatewayClassName: "istio",
			Labels:           map[string]string{"tier": "edge"},
			Annotations:      map[string]string{"example": "true"},
			Listeners: []cellv1alpha1.CellGatewayListener{{
				Name:     "http",
				Port:     80,
				Protocol: gatewayv1.HTTPProtocolType,
			}},
		}},
	}

	gw := &gatewayv1.Gateway{}
	MutateGateway(gw, router)

	if gw.Spec.GatewayClassName != "istio" {
		t.Fatalf("expected gateway class istio, got %s", gw.Spec.GatewayClassName)
	}
	if len(gw.Spec.Listeners) != 1 || string(gw.Spec.Listeners[0].Name) != "http" {
		t.Fatalf("expected listener http, got %+v", gw.Spec.Listeners)
	}
	if gw.Labels[constants.ManagedByLabel] != constants.OperatorName {
		t.Fatalf("expected managed-by label")
	}
	if gw.Labels["tier"] != "edge" {
		t.Fatalf("expected custom label propagated")
	}
}

func TestMutateGatewayNamespace(t *testing.T) {
	router := &cellv1alpha1.CellRouter{ObjectMeta: metav1.ObjectMeta{Name: "global"}}
	namespace := &corev1.Namespace{}

	MutateGatewayNamespace(namespace, router)

	if namespace.Labels[constants.ManagedByLabel] != constants.OperatorName {
		t.Fatalf("expected managed-by label")
	}
	if namespace.Labels[constants.RouterNameLabel] != "global" {
		t.Fatalf("expected router label")
	}
}

func TestMutateReferenceGrant(t *testing.T) {
	router := &cellv1alpha1.CellRouter{ObjectMeta: metav1.ObjectMeta{Name: "global"}}
	backend := BackendTarget{Name: "entry", Namespace: "payments", Port: 8080, CellRef: "payments"}
	grant := &gatewayv1beta1.ReferenceGrant{}

	MutateReferenceGrant(grant, router, "payments", "payments", "cell-routing", backend)

	if len(grant.Spec.From) != 1 {
		t.Fatalf("expected one reference source")
	}
	if string(grant.Spec.From[0].Namespace) != "cell-routing" {
		t.Fatalf("expected gateway namespace to be granted")
	}
	if len(grant.Spec.To) != 1 {
		t.Fatalf("expected one reference target")
	}
	if grant.Spec.To[0].Name == nil || string(*grant.Spec.To[0].Name) != "entry" {
		t.Fatalf("expected backend service entry to be granted")
	}
	if grant.Labels[constants.CellNameLabel] != "payments" {
		t.Fatalf("expected cell label")
	}
}

func TestMutateHTTPRoute(t *testing.T) {
	router := &cellv1alpha1.CellRouter{ObjectMeta: metav1.ObjectMeta{Name: "global"},
		Spec: cellv1alpha1.CellRouterSpec{Gateway: cellv1alpha1.CellGatewaySpec{Name: "ingress", Namespace: "cell-routing"}},
	}
	routeSpec := cellv1alpha1.CellRouteSpec{
		Name:          "payments",
		CellRef:       "payments",
		ListenerNames: []string{"http"},
		Hostnames:     []gatewayv1.Hostname{"api.example.com"},
		PathMatch: &cellv1alpha1.HTTPPathMatch{
			Value: "/payments",
		},
	}

	backend := BackendTarget{Name: "entry", Namespace: "payments", Port: 8080, CellRef: "payments"}

	httpRoute := &gatewayv1.HTTPRoute{}
	MutateHTTPRoute(httpRoute, router, routeSpec, "cell-routing", []BackendTarget{backend}, "")

	if len(httpRoute.Spec.CommonRouteSpec.ParentRefs) != 1 {
		t.Fatalf("expected one parent ref")
	}
	parent := httpRoute.Spec.CommonRouteSpec.ParentRefs[0]
	if string(parent.Name) != "ingress" || string(*parent.SectionName) != "http" {
		t.Fatalf("unexpected parent ref %v", parent)
	}
	if len(httpRoute.Spec.Rules) != 1 || len(httpRoute.Spec.Rules[0].BackendRefs) != 1 {
		t.Fatalf("unexpected backend refs %+v", httpRoute.Spec.Rules)
	}
	backendRef := httpRoute.Spec.Rules[0].BackendRefs[0]
	if string(backendRef.BackendRef.Name) != "entry" {
		t.Fatalf("expected backend service entry")
	}
	if httpRoute.Labels[constants.RouterNameLabel] != router.Name {
		t.Fatalf("expected router label")
	}
}

func TestReferenceGrantName(t *testing.T) {
	name := ReferenceGrantName("payments-route", BackendTarget{Name: "entry", Namespace: "payments"})
	if name == "" || name == "payments-route-backend" {
		t.Fatalf("expected hashed reference grant name, got %q", name)
	}
}

func TestBuildMatchesVariants(t *testing.T) {
	// headers only
	spec := cellv1alpha1.CellRouteSpec{
		HeaderMatches: []cellv1alpha1.HTTPHeaderMatch{{Name: "X-Test", Value: "1"}},
	}
	if matches := buildMatches(spec); len(matches) != 1 || len(matches[0].Headers) != 1 {
		t.Fatalf("expected header match to be included")
	}

	// query params only
	spec = cellv1alpha1.CellRouteSpec{
		QueryParamMatches: []cellv1alpha1.HTTPQueryParamMatch{{Name: "user", Value: "42"}},
	}
	if matches := buildMatches(spec); len(matches) != 1 || len(matches[0].QueryParams) != 1 {
		t.Fatalf("expected query match to be included")
	}

	// no matches should produce empty slice
	if matches := buildMatches(cellv1alpha1.CellRouteSpec{}); len(matches) != 0 {
		t.Fatalf("expected no matches when spec empty")
	}
}
