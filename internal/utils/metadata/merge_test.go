package metadata

import "testing"

func TestMerge(t *testing.T) {
	cases := map[string]struct {
		dst          map[string]string
		src          map[string]string
		protected    []string
		expectResult map[string]string
	}{
		"merges new values": {
			dst:          map[string]string{"a": "1"},
			src:          map[string]string{"b": "2"},
			expectResult: map[string]string{"a": "1", "b": "2"},
		},
		"creates map when dst nil": {
			src:          map[string]string{"a": "1"},
			expectResult: map[string]string{"a": "1"},
		},
		"preserves protected keys": {
			dst:          map[string]string{"keep": "old"},
			src:          map[string]string{"keep": "new", "other": "value"},
			protected:    []string{"keep"},
			expectResult: map[string]string{"keep": "old", "other": "value"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			result := Merge(tc.dst, tc.src, tc.protected...)
			if len(result) != len(tc.expectResult) {
				t.Fatalf("expected len=%d, got %d", len(tc.expectResult), len(result))
			}
			for k, v := range tc.expectResult {
				if result[k] != v {
					t.Fatalf("expected key %s to be %q, got %q", k, v, result[k])
				}
			}
		})
	}
}
