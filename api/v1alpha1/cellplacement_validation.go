package v1alpha1

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var decimalValuePattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// Validate checks whether the placement uses a supported selector configuration.
func (p *CellPlacement) Validate() error {
	return p.Spec.Validate()
}

// Validate checks whether the placement specification is internally consistent.
func (s *CellPlacementSpec) Validate() error {
	if strings.TrimSpace(s.RouterRef) == "" {
		return fmt.Errorf("routerRef is required")
	}
	if len(s.Selectors) == 0 {
		return fmt.Errorf("at least one selector is required")
	}
	if len(s.Destinations) == 0 {
		return fmt.Errorf("at least one destination is required")
	}

	seenSources := map[string]struct{}{}
	var pathRegex string

	for idx := range s.Selectors {
		selector := s.Selectors[idx]
		if err := selector.Validate(); err != nil {
			return fmt.Errorf("selector %d: %w", idx, err)
		}

		sourceKey := selector.Source.key()
		if _, exists := seenSources[sourceKey]; exists {
			return fmt.Errorf("selector %d duplicates source %q", idx, sourceKey)
		}
		seenSources[sourceKey] = struct{}{}

		if selector.Source.Type == SelectorSourcePathCapture {
			if pathRegex == "" {
				pathRegex = selector.Source.PathRegex
			} else if pathRegex != selector.Source.PathRegex {
				return fmt.Errorf("all PathCapture selectors must share the same pathRegex")
			}
		}
	}

	return nil
}

// Validate checks whether a selector can be compiled into a route match.
func (s *CellPlacementSelector) Validate() error {
	if err := s.Source.Validate(); err != nil {
		return err
	}

	switch s.Operator {
	case SelectorOperatorExact, SelectorOperatorPrefix, SelectorOperatorSuffix:
		if s.Value == "" {
			return fmt.Errorf("value is required for %s selectors", s.Operator)
		}
		if s.Range != nil {
			return fmt.Errorf("range must not be set for %s selectors", s.Operator)
		}
	case SelectorOperatorRange:
		if s.Range == nil {
			return fmt.Errorf("range is required for Range selectors")
		}
		if err := s.Range.Validate(); err != nil {
			return err
		}
		if s.Value != "" {
			return fmt.Errorf("value must not be set for Range selectors")
		}
	default:
		return fmt.Errorf("unsupported selector operator %q", s.Operator)
	}

	return nil
}

// Validate checks whether a selector source is syntactically valid.
func (s *CellPlacementSelectorSource) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("source name is required")
	}

	switch s.Type {
	case SelectorSourceHeader, SelectorSourceQueryParam:
		if s.PathRegex != "" {
			return fmt.Errorf("pathRegex is only supported for PathCapture selectors")
		}
	case SelectorSourcePathCapture:
		if strings.TrimSpace(s.PathRegex) == "" {
			return fmt.Errorf("pathRegex is required for PathCapture selectors")
		}
		if !pathRegexHasNamedCapture(s.PathRegex, s.Name) {
			return fmt.Errorf("pathRegex must contain named capture %q", s.Name)
		}
	default:
		return fmt.Errorf("unsupported selector source type %q", s.Type)
	}

	return nil
}

// Validate checks whether the range is a valid inclusive unsigned decimal interval.
func (r *CellPlacementNumericRange) Validate() error {
	if r == nil {
		return fmt.Errorf("range is required")
	}
	if !decimalValuePattern.MatchString(r.Start) {
		return fmt.Errorf("range start must be an unsigned decimal")
	}
	if !decimalValuePattern.MatchString(r.End) {
		return fmt.Errorf("range end must be an unsigned decimal")
	}

	startValue, err := strconv.ParseUint(r.Start, 10, 64)
	if err != nil {
		return fmt.Errorf("range start must fit into uint64: %w", err)
	}
	endValue, err := strconv.ParseUint(r.End, 10, 64)
	if err != nil {
		return fmt.Errorf("range end must fit into uint64: %w", err)
	}
	if startValue > endValue {
		return fmt.Errorf("range start must be less than or equal to range end")
	}
	return nil
}

// pathRegexHasNamedCapture checks whether the regex defines the given named capture.
func pathRegexHasNamedCapture(pathRegex, captureName string) bool {
	compiled, err := regexp.Compile(pathRegex)
	if err != nil {
		return false
	}
	groupNames := compiled.SubexpNames()
	return slices.Contains(groupNames, captureName)
}

func (s *CellPlacementSelectorSource) key() string {
	if s.Type == SelectorSourcePathCapture {
		return fmt.Sprintf("%s:%s:%s", s.Type, s.Name, s.PathRegex)
	}
	return fmt.Sprintf("%s:%s", s.Type, s.Name)
}

// SourceKey returns a stable identifier for the selector source.
func (s *CellPlacementSelector) SourceKey() string {
	return s.Source.key()
}
