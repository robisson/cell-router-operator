package controller

import (
	"testing"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestRouteSpecFromPlacementUsesNativeAndRegexMatches(t *testing.T) {
	placement := &cellv1alpha1.CellPlacement{
		Spec: cellv1alpha1.CellPlacementSpec{
			RouterRef: "default-router",
			Selectors: []cellv1alpha1.CellPlacementSelector{
				{
					Source:   cellv1alpha1.CellPlacementSelectorSource{Type: cellv1alpha1.SelectorSourceHeader, Name: "X-Tenant"},
					Operator: cellv1alpha1.SelectorOperatorExact,
					Value:    "tenant-a",
				},
				{
					Source:   cellv1alpha1.CellPlacementSelectorSource{Type: cellv1alpha1.SelectorSourceQueryParam, Name: "plan"},
					Operator: cellv1alpha1.SelectorOperatorPrefix,
					Value:    "gold-",
				},
			},
			Destinations: []cellv1alpha1.CellRouteBackendRef{{CellRef: "payments-cell-1"}},
		},
	}

	spec, err := routeSpecFromPlacement(placement)
	if err != nil {
		t.Fatalf("expected placement to compile, got error: %v", err)
	}
	if len(spec.HeaderMatches) != 1 || spec.HeaderMatches[0].Type == nil || *spec.HeaderMatches[0].Type != gatewayv1.HeaderMatchExact {
		t.Fatalf("expected exact header match, got %+v", spec.HeaderMatches)
	}
	if len(spec.QueryParamMatches) != 1 || spec.QueryParamMatches[0].Type == nil || *spec.QueryParamMatches[0].Type != gatewayv1.QueryParamMatchRegularExpression {
		t.Fatalf("expected regex query match, got %+v", spec.QueryParamMatches)
	}
	if spec.QueryParamMatches[0].Value != `^(?:gold-.*)$` {
		t.Fatalf("unexpected regex value %q", spec.QueryParamMatches[0].Value)
	}
}

func TestRouteSpecFromPlacementCompilesPathCaptureSelectors(t *testing.T) {
	placement := &cellv1alpha1.CellPlacement{
		Spec: cellv1alpha1.CellPlacementSpec{
			RouterRef: "default-router",
			Selectors: []cellv1alpha1.CellPlacementSelector{
				{
					Source: cellv1alpha1.CellPlacementSelectorSource{
						Type:      cellv1alpha1.SelectorSourcePathCapture,
						Name:      "tenantID",
						PathRegex: `^/tenants/(?P<tenantID>[a-z0-9-]+)$`,
					},
					Operator: cellv1alpha1.SelectorOperatorSuffix,
					Value:    "-west",
				},
			},
			Destinations: []cellv1alpha1.CellRouteBackendRef{{CellRef: "payments-cell-2"}},
		},
	}

	spec, err := routeSpecFromPlacement(placement)
	if err != nil {
		t.Fatalf("expected placement to compile, got error: %v", err)
	}
	if spec.PathMatch == nil || spec.PathMatch.Type == nil || *spec.PathMatch.Type != gatewayv1.PathMatchRegularExpression {
		t.Fatalf("expected regex path match, got %+v", spec.PathMatch)
	}
	if spec.PathMatch.Value != `^/tenants/(?P<tenantID>.*-west)$` {
		t.Fatalf("unexpected path regex %q", spec.PathMatch.Value)
	}
}

func TestRouteSpecFromPlacementCompilesNumericRange(t *testing.T) {
	placement := &cellv1alpha1.CellPlacement{
		Spec: cellv1alpha1.CellPlacementSpec{
			RouterRef: "default-router",
			Selectors: []cellv1alpha1.CellPlacementSelector{
				{
					Source:   cellv1alpha1.CellPlacementSelectorSource{Type: cellv1alpha1.SelectorSourceQueryParam, Name: "shard"},
					Operator: cellv1alpha1.SelectorOperatorRange,
					Range: &cellv1alpha1.CellPlacementNumericRange{
						Start: "10",
						End:   "15",
					},
				},
			},
			Destinations: []cellv1alpha1.CellRouteBackendRef{{CellRef: "payments-cell-1"}},
		},
	}

	spec, err := routeSpecFromPlacement(placement)
	if err != nil {
		t.Fatalf("expected placement to compile, got error: %v", err)
	}
	if spec.QueryParamMatches[0].Value != `^(?:1[0-5])$` {
		t.Fatalf("unexpected range regex %q", spec.QueryParamMatches[0].Value)
	}
}

func TestNumericRangeRegex(t *testing.T) {
	tests := map[string]struct {
		start    string
		end      string
		expected string
	}{
		"single digit":        {start: "3", end: "8", expected: `[3-8]`},
		"same prefix":         {start: "10", end: "15", expected: `1[0-5]`},
		"cross prefix":        {start: "15", end: "23", expected: `1[5-9]|2[0-3]`},
		"multi length values": {start: "95", end: "102", expected: `10[0-2]|9[5-9]`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			pattern, err := numericRangeRegex(tc.start, tc.end)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pattern != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, pattern)
			}
		})
	}
}
