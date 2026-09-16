package i18n

import "testing"

// TestCatalog_AllKeysHaveBothLanguages is the actual enforcement of the "every
// key has both languages" documentation rule (design.md D2) — not the
// render-time missingKeyMarker/ES-fallback, which are only backstops. A
// future PR that adds a Key with an empty ES or EN field fails `go test
// ./internal/gateway/...` here, for free, with no reviewer having to manually
// check every new catalog entry.
func TestCatalog_AllKeysHaveBothLanguages(t *testing.T) {
	for key, e := range catalog {
		t.Run(string(key), func(t *testing.T) {
			if e.ES == "" {
				t.Errorf("catalog[%q].ES is empty — every key needs a Spanish translation", key)
			}
			if e.EN == "" {
				t.Errorf("catalog[%q].EN is empty — every key needs an English translation", key)
			}
		})
	}
}
