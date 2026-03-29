package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestCellDeepCopy(t *testing.T) {
	original := &Cell{
		Spec: CellSpec{
			Namespace:  "payments",
			Entrypoint: CellEntrypointSpec{ServiceName: "entry", Port: 80},
		},
		Status: CellStatus{Namespace: "payments", EntrypointService: "payments/entry"},
	}
	clone := original.DeepCopy()
	if clone == original {
		t.Fatalf("DeepCopy returned the same pointer")
	}
	if clone.Spec.Entrypoint.ServiceName != original.Spec.Entrypoint.ServiceName {
		t.Fatalf("expected service name to match")
	}

	into := &Cell{}
	original.DeepCopyInto(into)
	if into.Status.Namespace != original.Status.Namespace {
		t.Fatalf("expected namespace copy")
	}

	if _, ok := original.DeepCopyObject().(*Cell); !ok {
		t.Fatalf("expected DeepCopyObject to return *Cell")
	}
}

func TestCellListDeepCopy(t *testing.T) {
	original := &CellList{Items: []Cell{{Spec: CellSpec{Namespace: "fast"}}}}
	clone := original.DeepCopy()
	if len(clone.Items) != 1 {
		t.Fatalf("expected items to be copied")
	}

	into := &CellList{}
	original.DeepCopyInto(into)
	if len(into.Items) != 1 {
		t.Fatalf("expected DeepCopyInto to copy items")
	}
}

func TestCellRouterDeepCopy(t *testing.T) {
	original := &CellRouter{
		Spec: CellRouterSpec{
			Gateway: CellGatewaySpec{Name: "gw", Namespace: "routing", GatewayClassName: "klass"},
			Routes:  []CellRouteSpec{{Name: "route", CellRef: "cell"}},
		},
	}
	clone := original.DeepCopy()
	if clone == original || len(clone.Spec.Routes) != 1 {
		t.Fatalf("expected deep copy of routes")
	}

	into := &CellRouter{}
	original.DeepCopyInto(into)
	if into.Spec.Gateway.Name != original.Spec.Gateway.Name {
		t.Fatalf("expected gateway copy")
	}

	if _, ok := original.DeepCopyObject().(*CellRouter); !ok {
		t.Fatalf("expected DeepCopyObject to return *CellRouter")
	}
}

func TestCellRouterListDeepCopy(t *testing.T) {
	original := &CellRouterList{Items: []CellRouter{{Spec: CellRouterSpec{Gateway: CellGatewaySpec{Name: "gw"}}}}}
	clone := original.DeepCopy()
	if len(clone.Items) != 1 {
		t.Fatalf("expected items to be copied")
	}

	into := &CellRouterList{}
	original.DeepCopyInto(into)
	if len(into.Items) != 1 {
		t.Fatalf("expected DeepCopyInto to copy items")
	}

	if _, ok := original.DeepCopyObject().(*CellRouterList); !ok {
		t.Fatalf("expected DeepCopyObject to return *CellRouterList")
	}
}

func TestAdditionalDeepCopies(t *testing.T) {
	port := int32(8080)
	entry := &CellEntrypointSpec{Port: 80, TargetPort: &port}
	if entry.DeepCopy().TargetPort == nil {
		t.Fatalf("expected target port to be copied")
	}

	host := gatewayv1.Hostname("api.example.com")
	listener := &CellGatewayListener{Hostname: &host, TLS: &gatewayv1.ListenerTLSConfig{}}
	if listener.DeepCopy().Hostname == nil {
		t.Fatalf("expected hostname to be copied")
	}

	gwSpec := &CellGatewaySpec{
		Labels:      map[string]string{"env": "prod"},
		Annotations: map[string]string{"team": "infra"},
		Listeners:   []CellGatewayListener{*listener},
	}
	if len(gwSpec.DeepCopy().Listeners) != 1 {
		t.Fatalf("expected listeners to be copied")
	}

	pathType := gatewayv1.PathMatchExact
	headerType := gatewayv1.HeaderMatchExact
	queryType := gatewayv1.QueryParamMatchExact
	weight := int32(10)
	routeSpec := &CellRouteSpec{
		ListenerNames:     []string{"http"},
		Hostnames:         []gatewayv1.Hostname{host},
		PathMatch:         &HTTPPathMatch{Type: &pathType, Value: "/"},
		HeaderMatches:     []HTTPHeaderMatch{{Type: &headerType, Name: "X-Tenant", Value: "acme"}},
		QueryParamMatches: []HTTPQueryParamMatch{{Type: &queryType, Name: "user", Value: "42"}},
		Weight:            &weight,
	}
	if routeSpec.DeepCopy().Weight == nil {
		t.Fatalf("expected weight to be copied")
	}

	cellSpec := &CellSpec{
		NamespaceLabels:  map[string]string{"team": "payments"},
		WorkloadSelector: map[string]string{"app": "svc"},
		Entrypoint:       *entry,
	}
	if len(cellSpec.DeepCopy().NamespaceLabels) != 1 {
		t.Fatalf("expected namespace labels to be copied")
	}

	cellStatus := &CellStatus{Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}}
	if len(cellStatus.DeepCopy().Conditions) != 1 {
		t.Fatalf("expected conditions to be copied")
	}

	routerStatus := &CellRouterStatus{
		ManagedRoutes: []ManagedRouteStatus{{
			ListenerNames:      []string{"http"},
			LastTransitionTime: &metav1.Time{Time: time.Now()},
		}},
		Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse}},
	}
	if len(routerStatus.DeepCopy().ManagedRoutes) != 1 {
		t.Fatalf("expected managed routes to be copied")
	}

	headMatch := &HTTPHeaderMatch{Type: &headerType, Name: "X-Test", Value: "1"}
	if headMatch.DeepCopy().Type == nil {
		t.Fatalf("expected header match type to be copied")
	}

	if path := (&HTTPPathMatch{Type: &pathType, Value: "/"}).DeepCopy(); path.Type == nil {
		t.Fatalf("expected path match type to be copied")
	}

	if query := (&HTTPQueryParamMatch{Type: &queryType, Name: "id", Value: "1"}).DeepCopy(); query.Type == nil {
		t.Fatalf("expected query match type to be copied")
	}

	placementSpec := &CellPlacementSpec{
		RouterRef:     "default-router",
		ListenerNames: []string{"http"},
		Hostnames:     []gatewayv1.Hostname{host},
		Selectors: []CellPlacementSelector{{
			Source: CellPlacementSelectorSource{
				Type:      SelectorSourcePathCapture,
				Name:      "tenant",
				PathRegex: `^/tenants/(?P<tenant>[a-z0-9-]+)$`,
			},
			Operator: SelectorOperatorPrefix,
			Value:    "team-",
		}},
		Destinations: []CellRouteBackendRef{{CellRef: "payments-cell-1"}},
	}
	if clone := placementSpec.DeepCopy(); len(clone.Selectors) != 1 || clone.Selectors[0].Source.PathRegex == "" {
		t.Fatalf("expected selectors to be copied")
	}

	if selector := (&CellPlacementSelector{
		Source:   CellPlacementSelectorSource{Type: SelectorSourceHeader, Name: "X-Shard"},
		Operator: SelectorOperatorRange,
		Range:    &CellPlacementNumericRange{Start: "0", End: "511"},
	}).DeepCopy(); selector.Range == nil || selector.Range.End != "511" {
		t.Fatalf("expected selector range to be copied")
	}

	if managed := (&ManagedRouteStatus{ListenerNames: []string{"http"}, LastTransitionTime: &metav1.Time{Time: time.Now()}}).DeepCopy(); managed.LastTransitionTime == nil {
		t.Fatalf("expected managed route transition time to be copied")
	}
}
