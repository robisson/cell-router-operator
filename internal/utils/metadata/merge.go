package metadata

// Merge merges the key/value pairs from src into dst while preserving protected keys.
// When dst is nil a new map is created. Protected keys are left untouched even if present in src.
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
