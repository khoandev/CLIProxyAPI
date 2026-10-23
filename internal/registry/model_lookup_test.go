package registry

import (
	"testing"
)

// TestLookupStaticModelInfoMatchesCatalogScan proves the static lookup
// resolves the same model as a scan over the assembled catalogs.
//
// The lookup walks the catalog sources directly to avoid cloning whole
// catalogs, so it must still find the same entry, including the hard-coded
// Devin built-in that replaces a catalog entry with the same ID.
func TestLookupStaticModelInfoMatchesCatalogScan(t *testing.T) {
	data := getModels()
	catalogs := [][]*ModelInfo{
		data.Claude,
		data.Gemini,
		data.Vertex,
		data.AIStudio,
		data.CodexPro,
		data.Kimi,
		data.Antigravity,
		data.XAI,
		GetDevinModels(),
		GetMetaModels(),
	}

	var ids []string
	for _, models := range catalogs {
		for _, m := range models {
			if m != nil && m.ID != "" {
				ids = append(ids, m.ID)
			}
		}
	}
	ids = append(ids, devinBuiltinSWE16SlowID)
	if len(ids) == 0 {
		t.Fatal("no catalog models to check")
	}

	for _, id := range ids {
		want := findModelByID(id, catalogs...)
		got := LookupStaticModelInfo(id)
		if want == nil {
			if got != nil {
				t.Fatalf("LookupStaticModelInfo(%q) = %+v, want nil", id, got)
			}
			continue
		}
		if got == nil {
			t.Fatalf("LookupStaticModelInfo(%q) = nil, want %q", id, want.ID)
		}
		if got.ID != want.ID {
			t.Fatalf("LookupStaticModelInfo(%q) = %q, want %q", id, got.ID, want.ID)
		}
	}

	if got := LookupStaticModelInfo("model-that-does-not-exist"); got != nil {
		t.Fatalf("LookupStaticModelInfo(unknown) = %+v, want nil", got)
	}
}
