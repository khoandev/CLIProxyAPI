package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigOptional_DefaultSwitchPreviewModelTrue(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 8317\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := LoadConfigOptional(configPath, false)
	if err != nil {
		t.Fatalf("LoadConfigOptional() error = %v", err)
	}
	if !cfg.QuotaExceeded.SwitchPreviewModel {
		t.Fatal("expected quota-exceeded.switch-preview-model default true when omitted from YAML")
	}
}

func TestLoadConfigOptional_EmptyOptionalConfigDefaultsSwitchPreviewModel(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "missing.yaml")
	cfg, err := LoadConfigOptional(configPath, true)
	if err != nil {
		t.Fatalf("LoadConfigOptional(optional) error = %v", err)
	}
	if !cfg.QuotaExceeded.SwitchPreviewModel {
		t.Fatal("expected switch-preview-model true for empty optional config")
	}
}

func TestParseConfigBytes_DefaultSwitchPreviewModelTrue(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte("port: 8317\n"))
	if err != nil {
		t.Fatalf("ParseConfigBytes() error = %v", err)
	}
	if !cfg.QuotaExceeded.SwitchPreviewModel {
		t.Fatal("expected switch-preview-model default true in ParseConfigBytes")
	}
}

func TestParseConfigBytes_ExplicitFalseSwitchPreviewModel(t *testing.T) {
	cfg, err := ParseConfigBytes([]byte(`
quota-exceeded:
  switch-preview-model: false
`))
	if err != nil {
		t.Fatalf("ParseConfigBytes() error = %v", err)
	}
	if cfg.QuotaExceeded.SwitchPreviewModel {
		t.Fatal("expected explicit false to override default")
	}
}
