package handlers

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

// TestGetRequestDetailsWithOptions_AllowVideoModelFallback verifies that when
// allowVideoModel=true and the model name is empty (or unknown), getRequestDetailsWithOptions
// returns all registered providers instead of a model_not_found error.
func TestGetRequestDetailsWithOptions_AllowVideoModelFallback(t *testing.T) {
	t.Parallel()
	modelRegistry := registry.GetGlobalRegistry()
	now := time.Now().Unix()
	clientID := "test-video-fallback-xai"
	modelRegistry.RegisterClient(clientID, "xai", []*registry.ModelInfo{
		{ID: "grok-imagine-video", Created: now},
	})
	t.Cleanup(func() { modelRegistry.UnregisterClient(clientID) })

	h := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))

	providers, _, errMsg := h.getRequestDetailsWithOptions("", false, false, true)
	if errMsg != nil {
		t.Fatalf("expected no error with allowVideoModel=true and empty model, got: %v", errMsg.Error)
	}
	if len(providers) == 0 {
		t.Fatal("expected at least one provider, got empty slice")
	}
	found := false
	for _, p := range providers {
		if p == "xai" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected provider 'xai' in fallback list, got %v", providers)
	}
}

// TestGetRequestDetailsWithOptions_AllowVideoModelKnownModel verifies that when
// allowVideoModel=true but a known model is given, normal registry routing is used.
func TestGetRequestDetailsWithOptions_AllowVideoModelKnownModel(t *testing.T) {
	t.Parallel()
	modelRegistry := registry.GetGlobalRegistry()
	now := time.Now().Unix()
	clientID := "test-video-known-model"
	modelRegistry.RegisterClient(clientID, "xai", []*registry.ModelInfo{
		{ID: "grok-imagine-video-known", Created: now},
	})
	t.Cleanup(func() { modelRegistry.UnregisterClient(clientID) })

	h := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))

	providers, model, errMsg := h.getRequestDetailsWithOptions("grok-imagine-video-known", false, false, true)
	if errMsg != nil {
		t.Fatalf("unexpected error: %v", errMsg.Error)
	}
	if len(providers) != 1 || providers[0] != "xai" {
		t.Errorf("providers = %v, want [xai]", providers)
	}
	if model != "grok-imagine-video-known" {
		t.Errorf("model = %q, want grok-imagine-video-known", model)
	}
}

// TestGetRequestDetailsWithOptions_NoAllowVideoModelRejectsEmpty verifies that without
// allowVideoModel=true, an empty model name still returns a model_not_found error.
func TestGetRequestDetailsWithOptions_NoAllowVideoModelRejectsEmpty(t *testing.T) {
	t.Parallel()
	h := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))

	_, _, errMsg := h.getRequestDetailsWithOptions("", false, false, false)
	if errMsg == nil {
		t.Fatal("expected error for empty model without allowVideoModel, got nil")
	}
	if errMsg.StatusCode != 400 {
		t.Errorf("status = %d, want 400", errMsg.StatusCode)
	}
}

// TestGetRequestDetailsWithOptions_NoAllowVideoModelRejectsUnknown verifies that without
// allowVideoModel=true, an unknown model name returns a model_not_found error.
func TestGetRequestDetailsWithOptions_NoAllowVideoModelRejectsUnknown(t *testing.T) {
	t.Parallel()
	h := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, coreauth.NewManager(nil, nil, nil))

	_, _, errMsg := h.getRequestDetailsWithOptions("completely-unknown-video-model-xyz", false, false, false)
	if errMsg == nil {
		t.Fatal("expected error for unknown model without allowVideoModel, got nil")
	}
	if errMsg.StatusCode != 400 {
		t.Errorf("status = %d, want 400", errMsg.StatusCode)
	}
}

// TestGetAllProviders_Registry verifies GetAllProviders on a fresh registry instance.
func TestGetAllProviders_Registry(t *testing.T) {
	// Uses the global registry — register a unique client and clean up.
	modelRegistry := registry.GetGlobalRegistry()
	clientID := "test-get-all-providers-compat"
	modelRegistry.RegisterClient(clientID, "openai-compatibility", []*registry.ModelInfo{
		{ID: "sora-video-test"},
	})
	t.Cleanup(func() { modelRegistry.UnregisterClient(clientID) })

	providers := modelRegistry.GetAllProviders()
	found := false
	for _, p := range providers {
		if p == "openai-compatibility" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected openai-compatibility in GetAllProviders(), got %v", providers)
	}
}
