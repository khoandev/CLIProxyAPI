package auth

import (
	"testing"
)

func TestShouldAttemptAllProvidersFallback(t *testing.T) {
	t.Run("nil manager returns false", func(t *testing.T) {
		var m *Manager
		if m.shouldAttemptAllProvidersFallback() {
			t.Error("nil manager should return false")
		}
	})
	t.Run("non-nil manager returns true", func(t *testing.T) {
		m := NewManager(nil, nil, nil)
		if !m.shouldAttemptAllProvidersFallback() {
			t.Error("should be enabled by default")
		}
	})
}

func TestModelProvidersFallback(t *testing.T) {
	t.Run("no extra providers registered for model → current unchanged", func(t *testing.T) {
		result := modelProvidersFallback([]string{"gemini"}, "nonexistent-model-xyz")
		if len(result) != 1 || result[0] != "gemini" {
			t.Errorf("got %v, want [gemini]", result)
		}
	})
	t.Run("current providers preserved at front", func(t *testing.T) {
		result := modelProvidersFallback([]string{"gemini"}, "nonexistent-model-xyz")
		if len(result) == 0 || result[0] != "gemini" {
			t.Errorf("got %v, want gemini first", result)
		}
	})
	t.Run("no duplicates", func(t *testing.T) {
		result := modelProvidersFallback([]string{"gemini", "codex"}, "nonexistent-model-xyz")
		seen := make(map[string]int)
		for _, p := range result {
			seen[p]++
		}
		for p, count := range seen {
			if count > 1 {
				t.Errorf("duplicate provider %q (count=%d)", p, count)
			}
		}
	})
}
