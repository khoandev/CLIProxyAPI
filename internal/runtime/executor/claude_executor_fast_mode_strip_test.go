package executor

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/tidwall/gjson"
)

func TestStripClaudeFastModeIfDisabled_NilConfig(t *testing.T) {
	body := []byte(`{"speed":"fast","model":"claude-opus-4-5"}`)
	extras := []string{claudeFastModeBeta}
	outBody, outBetas := stripClaudeFastModeIfDisabled(nil, body, extras)
	// nil config: nothing stripped
	if v := gjson.GetBytes(outBody, "speed"); !v.Exists() || v.String() != "fast" {
		t.Errorf("expected speed=fast preserved with nil cfg, got %q", v.String())
	}
	if len(outBetas) != 1 || outBetas[0] != claudeFastModeBeta {
		t.Errorf("expected extraBetas preserved with nil cfg, got %v", outBetas)
	}
}

func TestStripClaudeFastModeIfDisabled_EnabledIsNoop(t *testing.T) {
	cfg := &config.Config{FastServiceTier: true}
	body := []byte(`{"speed":"fast","model":"claude-opus-4-5"}`)
	extras := []string{claudeFastModeBeta}
	outBody, outBetas := stripClaudeFastModeIfDisabled(cfg, body, extras)
	// fast-service-tier=true: nothing stripped
	if v := gjson.GetBytes(outBody, "speed"); !v.Exists() || v.String() != "fast" {
		t.Errorf("expected speed=fast preserved when FastServiceTier=true, got %q", v.String())
	}
	if len(outBetas) != 1 || outBetas[0] != claudeFastModeBeta {
		t.Errorf("expected extraBetas preserved when FastServiceTier=true, got %v", outBetas)
	}
}

func TestStripClaudeFastModeIfDisabled_StripsSpeedAndBeta(t *testing.T) {
	cfg := &config.Config{FastServiceTier: false}
	body := []byte(`{"speed":"fast","model":"claude-opus-4-5"}`)
	extras := []string{claudeFastModeBeta, "interleaved-thinking-2025-05-14"}
	outBody, outBetas := stripClaudeFastModeIfDisabled(cfg, body, extras)
	if v := gjson.GetBytes(outBody, "speed"); v.Exists() {
		t.Errorf("expected speed stripped when FastServiceTier=false, got %q", v.String())
	}
	if v := gjson.GetBytes(outBody, "model"); v.String() != "claude-opus-4-5" {
		t.Errorf("expected model preserved, got %q", v.String())
	}
	for _, b := range outBetas {
		if b == claudeFastModeBeta {
			t.Errorf("expected fast-mode beta stripped from extraBetas, still present: %v", outBetas)
		}
	}
	if len(outBetas) != 1 || outBetas[0] != "interleaved-thinking-2025-05-14" {
		t.Errorf("expected other betas preserved, got %v", outBetas)
	}
}

func TestStripClaudeFastModeIfDisabled_NoSpeedNoFastBeta(t *testing.T) {
	cfg := &config.Config{FastServiceTier: false}
	body := []byte(`{"model":"claude-opus-4-5"}`)
	extras := []string{"interleaved-thinking-2025-05-14"}
	outBody, outBetas := stripClaudeFastModeIfDisabled(cfg, body, extras)
	if v := gjson.GetBytes(outBody, "speed"); v.Exists() {
		t.Errorf("expected no speed field, got %q", v.String())
	}
	if len(outBetas) != 1 || outBetas[0] != "interleaved-thinking-2025-05-14" {
		t.Errorf("expected betas unchanged, got %v", outBetas)
	}
}

func TestStripClaudeFastModeIfDisabled_SpeedCaseInsensitive(t *testing.T) {
	cfg := &config.Config{FastServiceTier: false}
	body := []byte(`{"speed":"FAST","model":"claude-opus-4-5"}`)
	outBody, _ := stripClaudeFastModeIfDisabled(cfg, body, nil)
	if v := gjson.GetBytes(outBody, "speed"); v.Exists() {
		t.Errorf("expected speed stripped for FAST (case-insensitive), got %q", v.String())
	}
}

func TestStripClaudeFastModeIfDisabled_SpeedNotFastPreserved(t *testing.T) {
	cfg := &config.Config{FastServiceTier: false}
	body := []byte(`{"speed":"normal","model":"claude-opus-4-5"}`)
	outBody, _ := stripClaudeFastModeIfDisabled(cfg, body, nil)
	if v := gjson.GetBytes(outBody, "speed"); !v.Exists() || v.String() != "normal" {
		t.Errorf("expected non-fast speed preserved, got %q", v.String())
	}
}

func TestStripFastModeBetaFromBetaString_NoFastBeta(t *testing.T) {
	input := "claude-code-20250219,interleaved-thinking-2025-05-14"
	out := stripFastModeBetaFromBetaString(input)
	if out != input {
		t.Errorf("expected unchanged when no fast beta, got %q", out)
	}
}

func TestStripFastModeBetaFromBetaString_OnlyFastBeta(t *testing.T) {
	out := stripFastModeBetaFromBetaString(claudeFastModeBeta)
	if out != "" {
		t.Errorf("expected empty string when only fast beta, got %q", out)
	}
}

func TestStripFastModeBetaFromBetaString_FastBetaAtStart(t *testing.T) {
	input := claudeFastModeBeta + ",interleaved-thinking-2025-05-14,claude-code-20250219"
	out := stripFastModeBetaFromBetaString(input)
	if strings.Contains(out, claudeFastModeBeta) {
		t.Errorf("expected fast beta removed from start, got %q", out)
	}
	if !strings.Contains(out, "interleaved-thinking-2025-05-14") || !strings.Contains(out, "claude-code-20250219") {
		t.Errorf("expected other betas preserved, got %q", out)
	}
}

func TestStripFastModeBetaFromBetaString_FastBetaAtEnd(t *testing.T) {
	input := "claude-code-20250219,interleaved-thinking-2025-05-14," + claudeFastModeBeta
	out := stripFastModeBetaFromBetaString(input)
	if strings.Contains(out, claudeFastModeBeta) {
		t.Errorf("expected fast beta removed from end, got %q", out)
	}
	if !strings.Contains(out, "interleaved-thinking-2025-05-14") {
		t.Errorf("expected other betas preserved, got %q", out)
	}
}

func TestStripFastModeBetaFromBetaString_FastBetaInMiddle(t *testing.T) {
	input := "claude-code-20250219," + claudeFastModeBeta + ",interleaved-thinking-2025-05-14"
	out := stripFastModeBetaFromBetaString(input)
	if strings.Contains(out, claudeFastModeBeta) {
		t.Errorf("expected fast beta removed from middle, got %q", out)
	}
	if !strings.Contains(out, "claude-code-20250219") || !strings.Contains(out, "interleaved-thinking-2025-05-14") {
		t.Errorf("expected other betas preserved, got %q", out)
	}
}

func TestStripFastModeBetaFromBetaString_EmptyString(t *testing.T) {
	out := stripFastModeBetaFromBetaString("")
	if out != "" {
		t.Errorf("expected empty string for empty input, got %q", out)
	}
}
