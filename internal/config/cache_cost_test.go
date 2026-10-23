package config

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/cachecost"
)

func ratioOf(t *testing.T, value *float64) float64 {
	t.Helper()
	resolved := normalizeCacheHitCostRatio(value)
	if resolved == nil {
		t.Fatal("normalized ratio must not be nil")
	}
	return *resolved
}

func TestNormalizeCacheHitCostRatio(t *testing.T) {
	zero := 0.0
	one := 1.0
	half := 0.5
	negative := -0.5
	tooLarge := 1.5

	cases := []struct {
		name  string
		value *float64
		want  float64
	}{
		{name: "unset falls back to default", value: nil, want: cachecost.Default},
		{name: "zero is preserved", value: &zero, want: 0},
		{name: "one is preserved", value: &one, want: 1},
		{name: "half is preserved", value: &half, want: 0.5},
		{name: "negative falls back to default", value: &negative, want: cachecost.Default},
		{name: "above one falls back to default", value: &tooLarge, want: cachecost.Default},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ratioOf(t, tc.value); got != tc.want {
				t.Fatalf("ratio = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCacheHitCostRatioValueFallsBackWhenUnset(t *testing.T) {
	cfg := &Config{}
	if got := cfg.CacheHitCostRatioValue(); got != cachecost.Default {
		t.Fatalf("unset ratio = %v, want %v", got, cachecost.Default)
	}
}

func TestParseConfigBytesResolvesCacheHitCostRatio(t *testing.T) {
	cases := []struct {
		name string
		body string
		want float64
	}{
		{name: "absent defaults to 0.9", body: "port: 8317\n", want: cachecost.Default},
		{name: "explicit zero drops cache savings", body: "port: 8317\ncache-hit-cost-ratio: 0\n", want: 0},
		{name: "explicit one keeps upstream", body: "port: 8317\ncache-hit-cost-ratio: 1\n", want: 1},
		{name: "half is preserved", body: "port: 8317\ncache-hit-cost-ratio: 0.5\n", want: 0.5},
		{name: "out of range falls back", body: "port: 8317\ncache-hit-cost-ratio: 3\n", want: cachecost.Default},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseConfigBytes([]byte(tc.body))
			if err != nil {
				t.Fatalf("parse config: %v", err)
			}
			if got := cfg.CacheHitCostRatioValue(); got != tc.want {
				t.Fatalf("ratio = %v, want %v", got, tc.want)
			}
		})
	}
}
