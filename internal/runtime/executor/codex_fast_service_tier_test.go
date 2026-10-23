package executor

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/tidwall/gjson"
)

func TestApplyCodexFastServiceTier_Disabled(t *testing.T) {
	cfg := &config.Config{}
	body := []byte(`{"model":"gpt-5-codex"}`)
	result := applyCodexFastServiceTier(cfg, body)
	if v := gjson.GetBytes(result, "service_tier"); v.Exists() {
		t.Errorf("expected no service_tier when FastServiceTier=false, got %q", v.String())
	}
}

func TestApplyCodexFastServiceTier_Enabled(t *testing.T) {
	cfg := &config.Config{FastServiceTier: true}
	body := []byte(`{"model":"gpt-5-codex"}`)
	result := applyCodexFastServiceTier(cfg, body)
	if v := gjson.GetBytes(result, "service_tier"); !v.Exists() || v.String() != "priority" {
		t.Errorf("expected service_tier=priority, got %q", v.String())
	}
}

func TestApplyCodexFastServiceTier_OverwritesExisting(t *testing.T) {
	cfg := &config.Config{FastServiceTier: true}
	body := []byte(`{"model":"gpt-5-codex","service_tier":"fast"}`)
	result := applyCodexFastServiceTier(cfg, body)
	if v := gjson.GetBytes(result, "service_tier"); v.String() != "priority" {
		t.Errorf("expected service_tier=priority (overwrite), got %q", v.String())
	}
}

func TestApplyCodexFastServiceTier_NilConfig(t *testing.T) {
	body := []byte(`{"model":"gpt-5-codex"}`)
	result := applyCodexFastServiceTier(nil, body)
	if v := gjson.GetBytes(result, "service_tier"); v.Exists() {
		t.Errorf("expected no service_tier when cfg is nil, got %q", v.String())
	}
}

func TestApplyCodexFastServiceTier_EmptyBody(t *testing.T) {
	cfg := &config.Config{FastServiceTier: true}
	result := applyCodexFastServiceTier(cfg, nil)
	if result != nil {
		t.Errorf("expected nil for nil body, got %s", string(result))
	}
}

func TestApplyCodexFastServiceTier_DisabledStripsExistingServiceTier(t *testing.T) {
	cfg := &config.Config{FastServiceTier: false}
	body := []byte(`{"model":"gpt-5-codex","service_tier":"flex"}`)
	result := applyCodexFastServiceTier(cfg, body)
	if v := gjson.GetBytes(result, "service_tier"); v.Exists() {
		t.Errorf("expected service_tier removed when FastServiceTier=false, got %q", v.String())
	}
}

func TestApplyCodexFastServiceTier_DisabledStripsServiceTierPriority(t *testing.T) {
	cfg := &config.Config{FastServiceTier: false}
	body := []byte(`{"model":"gpt-5-codex","service_tier":"priority"}`)
	result := applyCodexFastServiceTier(cfg, body)
	if v := gjson.GetBytes(result, "service_tier"); v.Exists() {
		t.Errorf("expected service_tier removed when FastServiceTier=false, got %q", v.String())
	}
}

func TestApplyCodexFastServiceTier_DisabledNoServiceTierIsNoop(t *testing.T) {
	cfg := &config.Config{FastServiceTier: false}
	body := []byte(`{"model":"gpt-5-codex"}`)
	result := applyCodexFastServiceTier(cfg, body)
	if v := gjson.GetBytes(result, "service_tier"); v.Exists() {
		t.Errorf("expected no service_tier, got %q", v.String())
	}
	if v := gjson.GetBytes(result, "model"); v.String() != "gpt-5-codex" {
		t.Errorf("expected model preserved, got %q", v.String())
	}
}
