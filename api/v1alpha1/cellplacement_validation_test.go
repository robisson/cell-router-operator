package v1alpha1

import "testing"

func TestCellPlacementValidate(t *testing.T) {
	valid := &CellPlacement{
		Spec: CellPlacementSpec{
			RouterRef: "default-router",
			Selectors: []CellPlacementSelector{
				{
					Source: CellPlacementSelectorSource{
						Type:      SelectorSourcePathCapture,
						Name:      "tenantID",
						PathRegex: `^/tenants/(?P<tenantID>[a-z0-9-]+)$`,
					},
					Operator: SelectorOperatorPrefix,
					Value:    "team-",
				},
				{
					Source: CellPlacementSelectorSource{
						Type: SelectorSourceHeader,
						Name: "X-Shard",
					},
					Operator: SelectorOperatorRange,
					Range: &CellPlacementNumericRange{
						Start: "0",
						End:   "511",
					},
				},
			},
			Destinations: []CellRouteBackendRef{{CellRef: "payments-cell-1"}},
		},
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid placement, got error: %v", err)
	}
}

func TestCellPlacementValidateRejectsMissingNamedCapture(t *testing.T) {
	placement := &CellPlacement{
		Spec: CellPlacementSpec{
			RouterRef: "default-router",
			Selectors: []CellPlacementSelector{{
				Source: CellPlacementSelectorSource{
					Type:      SelectorSourcePathCapture,
					Name:      "tenantID",
					PathRegex: `^/tenants/(?P<other>[a-z0-9-]+)$`,
				},
				Operator: SelectorOperatorExact,
				Value:    "tenant-a",
			}},
			Destinations: []CellRouteBackendRef{{CellRef: "payments-cell-1"}},
		},
	}

	if err := placement.Validate(); err == nil {
		t.Fatalf("expected missing named capture to fail validation")
	}
}

func TestCellPlacementValidateRejectsInvalidRanges(t *testing.T) {
	tests := []CellPlacementNumericRange{
		{Start: "not-a-number", End: "10"},
		{Start: "10", End: "not-a-number"},
		{Start: "10", End: "9"},
	}

	for _, tc := range tests {
		selector := CellPlacementSelector{
			Source:   CellPlacementSelectorSource{Type: SelectorSourceQueryParam, Name: "shard"},
			Operator: SelectorOperatorRange,
			Range:    &tc,
		}
		if err := selector.Validate(); err == nil {
			t.Fatalf("expected range %+v to fail validation", tc)
		}
	}
}
