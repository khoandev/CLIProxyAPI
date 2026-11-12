package config

import (
	"testing"
)

// TestCacheBudgetBytesConfigDecoding verifies that the cache budget is read
// from the configuration in both the flat and the version 8 layout.
func TestCacheBudgetBytesConfigDecoding(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want int64
	}{
		{
			name: "unset keeps the automatic budget",
			yaml: "config-version: 8\nserver:\n  port: 8317\n",
			want: 0,
		},
		{
			name: "explicit budget in the server section",
			yaml: "config-version: 8\nserver:\n  cache-budget-bytes: 67108864\n",
			want: 67108864,
		},
		{
			name: "explicit budget in the flat layout",
			yaml: "config-version: 8\ncache-budget-bytes: 33554432\n",
			want: 33554432,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseConfigBytes([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("ParseConfigBytes() error = %v", err)
			}
			if cfg.CacheBudgetBytes != tc.want {
				t.Fatalf("CacheBudgetBytes = %d, want %d", cfg.CacheBudgetBytes, tc.want)
			}
		})
	}
}
