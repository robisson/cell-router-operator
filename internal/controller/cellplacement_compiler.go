package controller

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	cellv1alpha1 "github.com/robisson/cell-router-operator/api/v1alpha1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func routeSpecFromPlacement(placement *cellv1alpha1.CellPlacement) (cellv1alpha1.CellRouteSpec, error) {
	if err := placement.Validate(); err != nil {
		return cellv1alpha1.CellRouteSpec{}, err
	}

	spec := cellv1alpha1.CellRouteSpec{
		Name:          placement.Name,
		ListenerNames: append([]string(nil), placement.Spec.ListenerNames...),
		Hostnames:     append([]gatewayv1.Hostname(nil), placement.Spec.Hostnames...),
	}

	pathRegex, err := compilePathRegex(placement.Spec.Selectors)
	if err != nil {
		return cellv1alpha1.CellRouteSpec{}, err
	}
	if pathRegex != "" {
		pathType := gatewayv1.PathMatchRegularExpression
		spec.PathMatch = &cellv1alpha1.HTTPPathMatch{
			Type:  &pathType,
			Value: pathRegex,
		}
	}

	for _, selector := range placement.Spec.Selectors {
		switch selector.Source.Type {
		case cellv1alpha1.SelectorSourceHeader:
			match, err := compileHeaderSelector(selector)
			if err != nil {
				return cellv1alpha1.CellRouteSpec{}, err
			}
			spec.HeaderMatches = append(spec.HeaderMatches, match)
		case cellv1alpha1.SelectorSourceQueryParam:
			match, err := compileQuerySelector(selector)
			if err != nil {
				return cellv1alpha1.CellRouteSpec{}, err
			}
			spec.QueryParamMatches = append(spec.QueryParamMatches, match)
		}
	}

	if len(placement.Spec.Destinations) > 0 {
		spec.CellRef = placement.Spec.Destinations[0].CellRef
		spec.Weight = placement.Spec.Destinations[0].Weight
		if len(placement.Spec.Destinations) > 1 {
			spec.AdditionalBackends = append([]cellv1alpha1.CellRouteBackendRef(nil), placement.Spec.Destinations[1:]...)
		}
	}

	return spec, nil
}

func compileHeaderSelector(selector cellv1alpha1.CellPlacementSelector) (cellv1alpha1.HTTPHeaderMatch, error) {
	match := cellv1alpha1.HTTPHeaderMatch{
		Name: gatewayv1.HTTPHeaderName(selector.Source.Name),
	}
	if selector.Operator == cellv1alpha1.SelectorOperatorExact {
		match.Value = selector.Value
		matchType := gatewayv1.HeaderMatchExact
		match.Type = &matchType
		return match, nil
	}

	regexValue, err := compileSelectorRegex(selector)
	if err != nil {
		return cellv1alpha1.HTTPHeaderMatch{}, err
	}
	match.Value = regexValue
	matchType := gatewayv1.HeaderMatchRegularExpression
	match.Type = &matchType
	return match, nil
}

func compileQuerySelector(selector cellv1alpha1.CellPlacementSelector) (cellv1alpha1.HTTPQueryParamMatch, error) {
	match := cellv1alpha1.HTTPQueryParamMatch{
		Name: gatewayv1.HTTPHeaderName(selector.Source.Name),
	}
	if selector.Operator == cellv1alpha1.SelectorOperatorExact {
		match.Value = selector.Value
		matchType := gatewayv1.QueryParamMatchExact
		match.Type = &matchType
		return match, nil
	}

	regexValue, err := compileSelectorRegex(selector)
	if err != nil {
		return cellv1alpha1.HTTPQueryParamMatch{}, err
	}
	match.Value = regexValue
	matchType := gatewayv1.QueryParamMatchRegularExpression
	match.Type = &matchType
	return match, nil
}

func compilePathRegex(selectors []cellv1alpha1.CellPlacementSelector) (string, error) {
	pathSelectors := make([]cellv1alpha1.CellPlacementSelector, 0)
	for _, selector := range selectors {
		if selector.Source.Type == cellv1alpha1.SelectorSourcePathCapture {
			pathSelectors = append(pathSelectors, selector)
		}
	}
	if len(pathSelectors) == 0 {
		return "", nil
	}

	pathRegex := pathSelectors[0].Source.PathRegex
	for _, selector := range pathSelectors {
		body, err := selectorRegexBody(selector)
		if err != nil {
			return "", err
		}
		pathRegex, err = replaceNamedCaptureBody(pathRegex, selector.Source.Name, body)
		if err != nil {
			return "", err
		}
	}
	return pathRegex, nil
}

func compileSelectorRegex(selector cellv1alpha1.CellPlacementSelector) (string, error) {
	body, err := selectorRegexBody(selector)
	if err != nil {
		return "", err
	}
	return `^(?:` + body + `)$`, nil
}

func selectorRegexBody(selector cellv1alpha1.CellPlacementSelector) (string, error) {
	switch selector.Operator {
	case cellv1alpha1.SelectorOperatorExact:
		return regexp.QuoteMeta(selector.Value), nil
	case cellv1alpha1.SelectorOperatorPrefix:
		return regexp.QuoteMeta(selector.Value) + ".*", nil
	case cellv1alpha1.SelectorOperatorSuffix:
		return ".*" + regexp.QuoteMeta(selector.Value), nil
	case cellv1alpha1.SelectorOperatorRange:
		return numericRangeRegex(selector.Range.Start, selector.Range.End)
	default:
		return "", fmt.Errorf("unsupported selector operator %q", selector.Operator)
	}
}

func validatePlacementOverlaps(placements []cellv1alpha1.CellPlacement) map[string]string {
	conflicts := map[string]string{}
	for i := 0; i < len(placements); i++ {
		for j := i + 1; j < len(placements); j++ {
			left := placements[i]
			right := placements[j]
			if placementsObviouslyOverlap(&left, &right) {
				reason := fmt.Sprintf("placement overlaps with %q", right.Name)
				conflicts[left.Name] = reason
				conflicts[right.Name] = fmt.Sprintf("placement overlaps with %q", left.Name)
			}
		}
	}
	return conflicts
}

func placementsObviouslyOverlap(left, right *cellv1alpha1.CellPlacement) bool {
	if !slices.Equal(sortedStrings(left.Spec.ListenerNames), sortedStrings(right.Spec.ListenerNames)) {
		return false
	}
	if !slices.Equal(sortedHostnames(left.Spec.Hostnames), sortedHostnames(right.Spec.Hostnames)) {
		return false
	}

	leftExact := selectorSignature(left.Spec.Selectors, false)
	rightExact := selectorSignature(right.Spec.Selectors, false)
	if leftExact == rightExact && selectorSignature(left.Spec.Selectors, true) == selectorSignature(right.Spec.Selectors, true) {
		return true
	}

	if leftExact != rightExact {
		return false
	}

	leftRanges := rangeSelectorsBySource(left.Spec.Selectors)
	rightRanges := rangeSelectorsBySource(right.Spec.Selectors)
	for sourceKey, leftRange := range leftRanges {
		rightRange, exists := rightRanges[sourceKey]
		if !exists {
			continue
		}
		if numericRangesIntersect(leftRange, rightRange) {
			return true
		}
	}
	return false
}

func selectorSignature(selectors []cellv1alpha1.CellPlacementSelector, includeRanges bool) string {
	parts := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		if selector.Operator == cellv1alpha1.SelectorOperatorRange {
			if !includeRanges {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s|%s|%s|%s", selector.SourceKey(), selector.Operator, selector.Range.Start, selector.Range.End))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s|%s|%s", selector.SourceKey(), selector.Operator, selector.Value))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func rangeSelectorsBySource(selectors []cellv1alpha1.CellPlacementSelector) map[string]cellv1alpha1.CellPlacementNumericRange {
	ranges := map[string]cellv1alpha1.CellPlacementNumericRange{}
	for _, selector := range selectors {
		if selector.Operator == cellv1alpha1.SelectorOperatorRange && selector.Range != nil {
			ranges[selector.SourceKey()] = *selector.Range
		}
	}
	return ranges
}

func numericRangesIntersect(left, right cellv1alpha1.CellPlacementNumericRange) bool {
	leftStart, _ := strconv.ParseUint(left.Start, 10, 64)
	leftEnd, _ := strconv.ParseUint(left.End, 10, 64)
	rightStart, _ := strconv.ParseUint(right.Start, 10, 64)
	rightEnd, _ := strconv.ParseUint(right.End, 10, 64)
	return leftStart <= rightEnd && rightStart <= leftEnd
}

func replaceNamedCaptureBody(pattern, captureName, replacement string) (string, error) {
	prefix := "(?P<" + captureName + ">"
	index := strings.Index(pattern, prefix)
	if index < 0 {
		return "", fmt.Errorf("pathRegex must contain named capture %q", captureName)
	}

	bodyStart := index + len(prefix)
	inClass := false
	escaped := false
	depth := 1
	for idx := bodyStart; idx < len(pattern); idx++ {
		ch := pattern[idx]
		if escaped {
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			escaped = true
		case '[':
			if !inClass {
				inClass = true
			}
		case ']':
			if inClass {
				inClass = false
			}
		case '(':
			if !inClass {
				depth++
			}
		case ')':
			if !inClass {
				depth--
				if depth == 0 {
					return pattern[:bodyStart] + replacement + pattern[idx:], nil
				}
			}
		}
	}
	return "", fmt.Errorf("pathRegex capture %q is not closed", captureName)
}

func numericRangeRegex(start, end string) (string, error) {
	if start == end {
		return regexp.QuoteMeta(start), nil
	}

	startValue, err := strconv.ParseUint(start, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid range start: %w", err)
	}
	endValue, err := strconv.ParseUint(end, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid range end: %w", err)
	}
	if startValue > endValue {
		return "", fmt.Errorf("range start must be less than or equal to range end")
	}

	patterns := make([]string, 0)
	if len(start) == len(end) {
		patterns = append(patterns, sameLengthRangePatterns(start, end)...)
	} else {
		patterns = append(patterns, sameLengthRangePatterns(start, strings.Repeat("9", len(start)))...)
		for length := len(start) + 1; length < len(end); length++ {
			patterns = append(patterns, anyNumberPattern(length))
		}
		patterns = append(patterns, sameLengthRangePatterns(minValueForLength(len(end)), end)...)
	}

	return strings.Join(uniqueStrings(patterns), "|"), nil
}

func sameLengthRangePatterns(start, end string) []string {
	if len(start) == 0 {
		return []string{""}
	}
	if start == end {
		return []string{regexp.QuoteMeta(start)}
	}
	if len(start) == 1 {
		return []string{digitRangePattern(start[0], end[0])}
	}
	if start[0] == end[0] {
		prefix := regexp.QuoteMeta(string(start[0]))
		return prependPattern(prefix, sameLengthRangePatterns(start[1:], end[1:]))
	}

	patterns := make([]string, 0, 3)
	patterns = append(patterns, prependPattern(regexp.QuoteMeta(string(start[0])), sameLengthRangePatterns(start[1:], strings.Repeat("9", len(start)-1)))...)
	if start[0]+1 <= end[0]-1 {
		patterns = append(patterns, digitRangePattern(start[0]+1, end[0]-1)+digitWildcard(len(start)-1))
	}
	patterns = append(patterns, prependPattern(regexp.QuoteMeta(string(end[0])), sameLengthRangePatterns(strings.Repeat("0", len(end)-1), end[1:]))...)
	return uniqueStrings(patterns)
}

func prependPattern(prefix string, patterns []string) []string {
	result := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		result = append(result, prefix+pattern)
	}
	return result
}

func digitRangePattern(start, end byte) string {
	if start == end {
		return regexp.QuoteMeta(string(start))
	}
	if start == '0' && end == '9' {
		return `\d`
	}
	return fmt.Sprintf("[%c-%c]", start, end)
}

func digitWildcard(length int) string {
	switch length {
	case 0:
		return ""
	case 1:
		return `\d`
	default:
		return fmt.Sprintf(`\d{%d}`, length)
	}
}

func anyNumberPattern(length int) string {
	if length == 1 {
		return `\d`
	}
	return `[1-9]` + digitWildcard(length-1)
}

func minValueForLength(length int) string {
	if length <= 1 {
		return "0"
	}
	return "1" + strings.Repeat("0", length-1)
}

func sortedStrings(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func sortedHostnames(values []gatewayv1.Hostname) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, string(value))
	}
	sort.Strings(result)
	return result
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	sort.Strings(values)
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
