package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type contextFailoverTestExecutor struct {
	id        string
	mu        sync.Mutex
	models    []string
	originals [][]byte
	err       error
}

func (e *contextFailoverTestExecutor) Identifier() string { return e.id }

func (e *contextFailoverTestExecutor) Execute(_ context.Context, _ *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.models = append(e.models, req.Model)
	e.originals = append(e.originals, append([]byte(nil), opts.OriginalRequest...))
	if e.err != nil {
		return cliproxyexecutor.Response{}, e.err
	}
	return cliproxyexecutor.Response{Payload: []byte("ok:" + e.id)}, nil
}

func (e *contextFailoverTestExecutor) ExecuteStream(_ context.Context, _ *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.models = append(e.models, req.Model)
	e.originals = append(e.originals, append([]byte(nil), opts.OriginalRequest...))
	ch := make(chan cliproxyexecutor.StreamChunk, 1)
	if e.err != nil {
		ch <- cliproxyexecutor.StreamChunk{Err: e.err}
	} else {
		ch <- cliproxyexecutor.StreamChunk{Payload: []byte("ok:" + e.id)}
	}
	close(ch)
	return &cliproxyexecutor.StreamResult{Chunks: ch}, nil
}

func (e *contextFailoverTestExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *contextFailoverTestExecutor) CountTokens(_ context.Context, _ *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (e *contextFailoverTestExecutor) HttpRequest(_ context.Context, _ *Auth, _ *http.Request) (*http.Response, error) {
	return nil, nil
}

func (e *contextFailoverTestExecutor) calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.models...)
}

type contextFailoverProvider struct {
	context int
	err     error
}

func setupContextFailoverManager(t *testing.T, clientID string, providers map[string]contextFailoverProvider) (*Manager, map[string]*contextFailoverTestExecutor) {
	t.Helper()
	reg := registry.GetGlobalRegistry()
	for provider, spec := range providers {
		authID := clientID + "-" + provider
		reg.RegisterClient(authID, provider, []*registry.ModelInfo{{ID: "goliath", ContextLength: spec.context}})
	}
	t.Cleanup(func() {
		for provider := range providers {
			reg.UnregisterClient(clientID + "-" + provider)
		}
	})
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(3, time.Second, 0)
	execs := map[string]*contextFailoverTestExecutor{}
	for provider, spec := range providers {
		exec := &contextFailoverTestExecutor{id: provider, err: spec.err}
		execs[provider] = exec
		m.RegisterExecutor(exec)
		auth := &Auth{ID: clientID + "-" + provider, Provider: provider}
		if provider != "codex" {
			auth.Metadata = map[string]any{"refresh_token": "test"}
		}
		if _, err := m.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth: %v", err)
		}
	}
	return m, execs
}

func contextFailoverExceeded(provider string, max, tokens int) error {
	return &ContextWindowExceededError{MaxContext: max, RequestTokens: tokens, Provider: provider, Model: "goliath"}
}

// 310k request: Codex (272k) fails, 200k provider skipped, 1M same-model
// provider serves the original request. No loops back to Codex.
func TestCodexContextFailoverDivertsToSameModelProvider(t *testing.T) {
	providers := map[string]contextFailoverProvider{
		"codex":      {context: 272000, err: contextFailoverExceeded("codex", 272000, 310000)},
		"prov-small": {context: 200000, err: contextFailoverExceeded("prov-small", 200000, 310000)},
		"prov-big":   {context: 1000000},
	}
	original := []byte(`{"model":"goliath","messages":[{"role":"user","content":"hi"}]}`)

	t.Run("non-stream", func(t *testing.T) {
		m, execs := setupContextFailoverManager(t, "ctx-failover-divert", providers)
		resp, err := m.Execute(context.Background(), []string{"codex"},
			cliproxyexecutor.Request{Model: "goliath", Payload: original},
			cliproxyexecutor.Options{OriginalRequest: original, Metadata: map[string]any{}})
		if err != nil {
			t.Fatalf("Execute() error = %v, want success", err)
		}
		if string(resp.Payload) != "ok:prov-big" {
			t.Errorf("Execute() payload = %q, want ok:prov-big", resp.Payload)
		}
		if got := execs["codex"].calls(); !slicesEqual(got, []string{"goliath"}) {
			t.Errorf("codex calls = %v, want single attempt (no loop)", got)
		}
		if got := execs["prov-small"].calls(); len(got) != 0 {
			t.Errorf("prov-small calls = %v, want skipped (200k < 310k)", got)
		}
		big := execs["prov-big"]
		if got := big.calls(); !slicesEqual(got, []string{"goliath"}) {
			t.Errorf("prov-big calls = %v, want [goliath]", got)
		}
		big.mu.Lock()
		defer big.mu.Unlock()
		if len(big.originals) != 1 || string(big.originals[0]) != string(original) {
			t.Errorf("prov-big original request not preserved")
		}
	})

	t.Run("stream", func(t *testing.T) {
		m, execs := setupContextFailoverManager(t, "ctx-failover-divert-stream", providers)
		result, err := m.ExecuteStream(context.Background(), []string{"codex"},
			cliproxyexecutor.Request{Model: "goliath", Payload: original},
			cliproxyexecutor.Options{OriginalRequest: original, Metadata: map[string]any{}})
		if err != nil {
			t.Fatalf("ExecuteStream() error = %v, want success", err)
		}
		if result == nil {
			t.Fatal("ExecuteStream() result = nil")
		}
		drainStreamResult(t, result)
		if got := execs["prov-big"].calls(); !slicesEqual(got, []string{"goliath"}) {
			t.Errorf("prov-big stream calls = %v, want [goliath]", got)
		}
		if got := execs["prov-small"].calls(); len(got) != 0 {
			t.Errorf("prov-small stream calls = %v, want skipped", got)
		}
	})
}

func TestCodexContextFailoverNoCandidate(t *testing.T) {
	providers := map[string]contextFailoverProvider{
		"codex":      {context: 272000, err: contextFailoverExceeded("codex", 272000, 310000)},
		"prov-small": {context: 200000, err: contextFailoverExceeded("prov-small", 200000, 310000)},
	}
	m, execs := setupContextFailoverManager(t, "ctx-failover-nocand", providers)
	_, err := m.Execute(context.Background(), []string{"codex"},
		cliproxyexecutor.Request{Model: "goliath"},
		cliproxyexecutor.Options{Metadata: map[string]any{}})
	if err == nil {
		t.Fatal("Execute() expected terminal error, got nil")
	}
	var authErr *Error
	if !errors.As(err, &authErr) || authErr == nil {
		t.Fatalf("Execute() error type = %T, want errors.As *Error", err)
	}
	if !errors.Is(err, ErrContextWindowExceeded) {
		t.Fatalf("Execute() error = %v, want errors.Is ErrContextWindowExceeded", err)
	}
	var contextErr *ContextWindowExceededError
	if !errors.As(err, &contextErr) || contextErr == nil {
		t.Fatalf("Execute() error = %v, want errors.As *ContextWindowExceededError", err)
	}
	if contextErr.RequestTokens != 310000 || contextErr.MaxContext != 272000 {
		t.Errorf("context counts = (%d,%d), want (310000,272000)", contextErr.RequestTokens, contextErr.MaxContext)
	}
	want := "Codex context limit exceeded at 310000 tokens and no configured ai-provider can handle the request context"
	if authErr.Message != want {
		t.Errorf("Execute() message = %q, want %q", authErr.Message, want)
	}
	if got := execs["prov-small"].calls(); len(got) != 0 {
		t.Errorf("prov-small calls = %v, want none attempted", got)
	}
}

func TestSelectContextFallbackLoopGuard(t *testing.T) {
	m, _ := setupContextFailoverManager(t, "ctx-failover-guard", map[string]contextFailoverProvider{
		"prov-big": {context: 1000000},
	})
	provider, model, ok := m.SelectContextFallback("goliath", 310000, map[string]bool{})
	if !ok || provider != "prov-big" || model != "goliath" {
		t.Fatalf("SelectContextFallback() = (%q,%q,%v), want (prov-big,goliath,true)", provider, model, ok)
	}
	exclude := map[string]bool{"codex": true, contextFallbackRouteKey("prov-big", "goliath"): true}
	if _, _, ok := m.SelectContextFallback("goliath", 310000, exclude); ok {
		t.Fatal("SelectContextFallback() with tried route excluded = ok, want false")
	}
	opts := withContextFallbackAttempted(cliproxyexecutor.Options{Metadata: map[string]any{}}, "prov-big", "goliath")
	if !contextFallbackExcludedRoutes(opts)["prov-big\x00goliath"] {
		t.Fatal("attempted route missing from per-request exclude set")
	}
}
