package metadata

// Merge overlays src onto dst while preserving keys that belong to the
// operator's ownership contract. This lets users add metadata without being
// able to remove labels or annotations that reconciliation depends on.
func Merge(dst map[string]string, src map[string]string, protectedKeys ...string) map[string]string {
	if len(src) == 0 {
		if dst == nil {
			return map[string]string{}
		}
		return dst
	}

	protected := make(map[string]struct{}, len(protectedKeys))
	for _, key := range protectedKeys {
		protected[key] = struct{}{}
	}

	if dst == nil {
		dst = make(map[string]string, len(src))
	}

	for k, v := range src {
		if _, ok := protected[k]; ok {
			continue
		}
		dst[k] = v
	}

	return dst
}
