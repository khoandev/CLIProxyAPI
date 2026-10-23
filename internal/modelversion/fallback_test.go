package modelversion

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

func registerTestModels(t *testing.T, clientID, provider string, ids ...string) {
	t.Helper()
	reg := registry.GetGlobalRegistry()
	models := make([]*registry.ModelInfo, 0, len(ids))
	for _, id := range ids {
		models = append(models, &registry.ModelInfo{ID: id})
	}
	reg.RegisterClient(clientID, provider, models)
	t.Cleanup(func() { reg.UnregisterClient(clientID) })
}

func TestNextFallbackModel(t *testing.T) {
	const client = "modelversion-fallback-test"
	registerTestModels(t, client, "openai",
		"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8",
		"claude-opus-5",
		"claude-sonnet-4-5", "claude-sonnet-4-6",
		"claude-sonnet-5",
		"glm-5", "glm-5.1", "glm-5.2",
		"kimi-k2.5", "kimi-k2.6", "kimi-k2.7", "kimi-k2.7-code", "kimi-k2.7-code-highspeed",
		"gpt-5.4", "gpt-5.4-mini", "gpt-5.5",
		"grok-4.3", "grok-4.5",
	)

	tests := []struct {
		in   string
		want string
	}{
		{"claude-opus-4-8", "claude-opus-4-7"},
		{"claude-opus-4-7", "claude-opus-4-6"},
		{"claude-opus-4-6", ""},
		{"claude-opus-5", "claude-opus-4-8"},
		{"claude-opus-5-1", "claude-opus-5"},
		{"claude-opus-5(high)", "claude-opus-4-8(high)"},
		{"claude-opus-4-8(high)", "claude-opus-4-7(high)"},
		{"claude-opus-4-7(16384)", "claude-opus-4-6(16384)"},
		{"claude-opus-4-8-20250801", "claude-opus-4-7"},
		{"claude-sonnet-4-6", "claude-sonnet-4-5"},
		{"claude-sonnet-4-6(high)", "claude-sonnet-4-5(high)"},
		{"claude-sonnet-4-5", ""},
		{"claude-sonnet-5", "claude-sonnet-4-6"},
		{"claude-sonnet-5(high)", "claude-sonnet-4-6(high)"},
		{"glm-5.2", "glm-5.1"},
		{"glm-5.1", "glm-5"},
		{"glm-5", ""},
		{"kimi-k2.6", "kimi-k2.5"},
		{"kimi-k2.6(8192)", "kimi-k2.5(8192)"},
		{"kimi-k2.7", "kimi-k2.6"},
		{"kimi-k2.7-code", "kimi-k2.6"},
		{"kimi-k2.7-code-highspeed", "kimi-k2.6"},
		{"kimi-k2.7(high)", "kimi-k2.6(high)"},
		{"kimi-k2.5", ""},
		{"gpt-5.5", "gpt-5.4"},
		{"gpt-5.6-terra", "gpt-5.4"},
		{"gpt-5.6-sol", "gpt-5.5"},
		{"gpt-5.6-luna", "gpt-5.4"},
		{"gpt-5.6-terra(high)", "gpt-5.4(high)"},
		{"gpt-5.6-sol-20260101", "gpt-5.5"},
		{"grok-4.5", "grok-4.3"},
		{"grok-4.5(high)", "grok-4.3(high)"},
		{"grok-4.3", ""},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := Next(tt.in, nil); got != tt.want {
				t.Fatalf("Next(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestChainFullFamilies(t *testing.T) {
	tests := []struct {
		name      string
		client    string
		models    []string
		request   string
		wantChain []string
	}{
		{
			name:      "glm-5.2 to glm-5",
			client:    "chain-glm",
			models:    []string{"glm-5", "glm-5.1", "glm-5.2"},
			request:   "glm-5.2",
			wantChain: []string{"glm-5.1", "glm-5"},
		},
		{
			name:      "kimi-k2.7 to kimi-k2.5",
			client:    "chain-kimi",
			models:    []string{"kimi-k2.5", "kimi-k2.6", "kimi-k2.7"},
			request:   "kimi-k2.7",
			wantChain: []string{"kimi-k2.6", "kimi-k2.5"},
		},
		{
			name:      "kimi-k2.7-code-highspeed",
			client:    "chain-kimi-code",
			models:    []string{"kimi-k2.6", "kimi-k2.7", "kimi-k2.7-code-highspeed"},
			request:   "kimi-k2.7-code-highspeed",
			wantChain: []string{"kimi-k2.6"},
		},
		{
			name:      "minimax-m3 to m2.7",
			client:    "chain-minimax",
			models:    []string{"minimax-m2.7", "minimax-m3"},
			request:   "minimax-m3",
			wantChain: []string{"minimax-m2.7"},
		},
		{
			name:      "claude-opus-4-8 chain",
			client:    "chain-claude",
			models:    []string{"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8"},
			request:   "claude-opus-4-8",
			wantChain: []string{"claude-opus-4-7", "claude-opus-4-6"},
		},
		{
			name:      "claude-opus-5 chain",
			client:    "chain-claude-5",
			models:    []string{"claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8", "claude-opus-5"},
			request:   "claude-opus-5",
			wantChain: []string{"claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6"},
		},
		{
			name:      "claude-sonnet-5 chain",
			client:    "chain-sonnet-5",
			models:    []string{"claude-sonnet-4-5", "claude-sonnet-4-6", "claude-sonnet-5"},
			request:   "claude-sonnet-5",
			wantChain: []string{"claude-sonnet-4-6", "claude-sonnet-4-5"},
		},
		{
			name:      "grok-4.5 to grok-4.3",
			client:    "chain-grok",
			models:    []string{"grok-4.3", "grok-4.5"},
			request:   "grok-4.5",
			wantChain: []string{"grok-4.3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registerTestModels(t, tt.client, "openai", tt.models...)
			got := Chain(tt.request, nil)
			if len(got) != len(tt.wantChain) {
				t.Fatalf("Chain(%q) = %v, want %v", tt.request, got, tt.wantChain)
			}
			for i := range tt.wantChain {
				if got[i] != tt.wantChain[i] {
					t.Fatalf("chain[%d] = %q, want %q", i, got[i], tt.wantChain[i])
				}
			}
		})
	}
}

func TestNextMinimaxFamily(t *testing.T) {
	const client = "minimax-next-test"
	registerTestModels(t, client, "openai", "minimax-m2.5", "minimax-m2.7", "minimax-m3")
	if got := Next("minimax-m3", nil); got != "minimax-m2.7" {
		t.Fatalf("Next(minimax-m3) = %q, want minimax-m2.7", got)
	}
	if got := Next("minimax-m2.7", nil); got != "minimax-m2.5" {
		t.Fatalf("Next(minimax-m2.7) = %q, want minimax-m2.5", got)
	}
}

func TestChainMultiHop(t *testing.T) {
	const client = "modelversion-chain-test"
	registerTestModels(t, client, "openai", "glm-5", "glm-5.1", "glm-5.2")
	chain := Chain("glm-5.2", nil)
	want := []string{"glm-5.1", "glm-5"}
	if len(chain) != len(want) {
		t.Fatalf("Chain() = %v, want %v", chain, want)
	}
	for i := range want {
		if chain[i] != want[i] {
			t.Fatalf("chain[%d] = %q, want %q", i, chain[i], want[i])
		}
	}
}

func TestNextRespectsProviderFilter(t *testing.T) {
	const clientA = "modelversion-provider-a"
	const clientB = "modelversion-provider-b"
	registerTestModels(t, clientA, "codex", "gpt-5.5")
	registerTestModels(t, clientB, "openai", "gpt-5.4")
	got := Next("gpt-5.5", []string{"codex"})
	if got != "" {
		t.Fatalf("Next with codex-only filter = %q, want empty", got)
	}
	got = Next("gpt-5.5", []string{"codex", "openai"})
	if got != "gpt-5.4" {
		t.Fatalf("Next with both providers = %q, want gpt-5.4", got)
	}
}

func TestNextGPT56CodenameFallback(t *testing.T) {
	t.Run("mapped missing continues normal rank", func(t *testing.T) {
		registerTestModels(t, "gpt56-codename-terra-mini", "openai", "gpt-5.4-mini")
		if got := Next("gpt-5.6-terra", nil); got != "gpt-5.4-mini" {
			t.Fatalf("Next(gpt-5.6-terra) = %q, want gpt-5.4-mini", got)
		}
	})
	t.Run("nothing usable returns empty", func(t *testing.T) {
		registerTestModels(t, "gpt56-codename-empty", "openai", "claude-opus-4-6")
		if got := Next("gpt-5.6-terra", nil); got != "" {
			t.Fatalf("Next(gpt-5.6-terra) = %q, want empty", got)
		}
	})
}

func TestChainGPT56Codename(t *testing.T) {
	tests := []struct {
		name      string
		client    string
		models    []string
		request   string
		wantChain []string
	}{
		{
			name:      "sol then gpt family",
			client:    "chain-gpt56-sol",
			models:    []string{"gpt-5.4", "gpt-5.4-mini", "gpt-5.5"},
			request:   "gpt-5.6-sol",
			wantChain: []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini"},
		},
		{
			name:      "terra then gpt family",
			client:    "chain-gpt56-terra",
			models:    []string{"gpt-5.4", "gpt-5.4-mini"},
			request:   "gpt-5.6-terra",
			wantChain: []string{"gpt-5.4", "gpt-5.4-mini"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registerTestModels(t, tt.client, "openai", tt.models...)
			got := Chain(tt.request, nil)
			if len(got) != len(tt.wantChain) {
				t.Fatalf("Chain(%q) = %v, want %v", tt.request, got, tt.wantChain)
			}
			for i := range tt.wantChain {
				if got[i] != tt.wantChain[i] {
					t.Fatalf("chain[%d] = %q, want %q", i, got[i], tt.wantChain[i])
				}
			}
		})
	}
}

func TestChainWithCandidatesPreservesFallbackOrder(t *testing.T) {
	got := chainWithCandidates("glm-5.2", []string{"glm-5", "glm-5.1", "glm-5.2"})
	want := []string{"glm-5.1", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("chainWithCandidates() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chain[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestChainWithCandidatesUsesSuppliedSnapshot proves the hot loop walks the
// supplied candidate snapshot only — it must never consult the global
// registry. These GLM ids use unrealistically high minor versions so they
// cannot collide with any concurrently registered test client; they are
// passed directly to chainWithCandidates and never registered anywhere. A
// registry-backed implementation would see zero matching candidates and
// return an empty chain, so a non-empty correct-order result proves the
// snapshot is used.
func TestChainWithCandidatesUsesSuppliedSnapshot(t *testing.T) {
	candidates := []string{"glm-5.97", "glm-5.98", "glm-5.99"}
	for _, id := range candidates {
		if reg := registry.GetGlobalRegistry(); reg.GetModelCount(id) != 0 {
			t.Fatalf("precondition: %q must not be globally registered", id)
		}
	}
	got := chainWithCandidates("glm-5.99", candidates)
	want := []string{"glm-5.98", "glm-5.97"}
	if len(got) != len(want) {
		t.Fatalf("chainWithCandidates() = %v, want %v (must use supplied snapshot, not registry)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chain[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
