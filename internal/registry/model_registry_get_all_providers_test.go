package registry

import "testing"

func TestGetAllProvidersEmpty(t *testing.T) {
	r := newTestModelRegistry()
	providers := r.GetAllProviders()
	if len(providers) != 0 {
		t.Fatalf("expected empty provider list, got %v", providers)
	}
}

func TestGetAllProvidersSingleProvider(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("client-1", "xai", []*ModelInfo{{ID: "grok-imagine-video"}})
	providers := r.GetAllProviders()
	if len(providers) != 1 || providers[0] != "xai" {
		t.Fatalf("expected [xai], got %v", providers)
	}
}

func TestGetAllProvidersDeduplicates(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("client-1", "xai", []*ModelInfo{{ID: "grok-imagine-video"}})
	r.RegisterClient("client-2", "xai", []*ModelInfo{{ID: "grok-imagine-video-1.5"}})
	providers := r.GetAllProviders()
	if len(providers) != 1 {
		t.Fatalf("expected 1 provider (deduplicated), got %v", providers)
	}
	if providers[0] != "xai" {
		t.Errorf("expected xai, got %q", providers[0])
	}
}

func TestGetAllProvidersSortedOutput(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("c1", "openai-compatibility", []*ModelInfo{{ID: "sora-2"}})
	r.RegisterClient("c2", "xai", []*ModelInfo{{ID: "grok-imagine-video"}})
	r.RegisterClient("c3", "codex", []*ModelInfo{{ID: "gpt-4o"}})
	providers := r.GetAllProviders()
	if len(providers) != 3 {
		t.Fatalf("expected 3 providers, got %v", providers)
	}
	// sort.Strings: codex < openai-compatibility < xai
	want := []string{"codex", "openai-compatibility", "xai"}
	for i, p := range providers {
		if p != want[i] {
			t.Errorf("providers[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestGetAllProvidersSkipsEmptyProvider(t *testing.T) {
	r := newTestModelRegistry()
	// Register a client without a provider (empty string).
	r.RegisterClient("c-empty", "", []*ModelInfo{{ID: "mystery-model"}})
	r.RegisterClient("c-real", "gemini", []*ModelInfo{{ID: "gemini-pro"}})
	providers := r.GetAllProviders()
	for _, p := range providers {
		if p == "" {
			t.Errorf("GetAllProviders should not include empty provider string")
		}
	}
	if len(providers) != 1 || providers[0] != "gemini" {
		t.Fatalf("expected [gemini], got %v", providers)
	}
}

func TestGetAllProvidersAfterUnregister(t *testing.T) {
	r := newTestModelRegistry()
	r.RegisterClient("c1", "xai", []*ModelInfo{{ID: "grok-imagine-video"}})
	r.RegisterClient("c2", "openai-compatibility", []*ModelInfo{{ID: "sora-2"}})

	before := r.GetAllProviders()
	if len(before) != 2 {
		t.Fatalf("expected 2 providers before unregister, got %v", before)
	}

	r.UnregisterClient("c2")
	after := r.GetAllProviders()
	if len(after) != 1 || after[0] != "xai" {
		t.Fatalf("expected [xai] after unregister, got %v", after)
	}
}
