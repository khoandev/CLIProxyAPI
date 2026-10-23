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

func TestHasCodexProvider(t *testing.T) {
	tests := []struct {
		name      string
		providers []string
		want      bool
	}{
		{"empty", []string{}, false},
		{"codex only", []string{"codex"}, true},
		{"codex uppercase", []string{"CODEX"}, true},
		{"codex with spaces", []string{"  codex  "}, true},
		{"other providers", []string{"openai", "anthropic"}, false},
		{"mixed with codex", []string{"openai", "codex"}, true},
		{"codex-api-key not matched", []string{"codex-api-key"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasCodexProvider(tt.providers)
			if got != tt.want {
				t.Errorf("hasCodexProvider(%v) = %v, want %v", tt.providers, got, tt.want)
			}
		})
	}
}

type codexFallbackTestExecutor struct {
	mu            sync.Mutex
	executeCalls  []string
	streamCalls   []string
	errorsByModel map[string]error
}

func (e *codexFallbackTestExecutor) Identifier() string { return "codex" }

func (e *codexFallbackTestExecutor) Execute(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.mu.Lock()
	e.executeCalls = append(e.executeCalls, req.Model)
	err := e.errorsByModel[req.Model]
	e.mu.Unlock()
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	return cliproxyexecutor.Response{Payload: []byte(req.Model)}, nil
}

func (e *codexFallbackTestExecutor) ExecuteStream(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	e.mu.Lock()
	e.streamCalls = append(e.streamCalls, req.Model)
	err := e.errorsByModel[req.Model]
	e.mu.Unlock()

	ch := make(chan cliproxyexecutor.StreamChunk, 1)
	if err != nil {
		ch <- cliproxyexecutor.StreamChunk{Err: err}
	} else {
		ch <- cliproxyexecutor.StreamChunk{Payload: []byte(req.Model)}
	}
	close(ch)
	return &cliproxyexecutor.StreamResult{Chunks: ch}, nil
}

func (e *codexFallbackTestExecutor) Refresh(ctx context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *codexFallbackTestExecutor) CountTokens(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, errors.New("not implemented")
}

func (e *codexFallbackTestExecutor) HttpRequest(ctx context.Context, auth *Auth, req *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func (e *codexFallbackTestExecutor) ExecuteCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.executeCalls...)
}

func (e *codexFallbackTestExecutor) StreamCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.streamCalls...)
}

func contextTooLargeError() error {
	return &Error{
		HTTPStatus: http.StatusRequestEntityTooLarge,
		Message:    `{"error":{"type":"invalid_request_error","code":"context_too_large","message":"Your input exceeds the context window of this model."}}`,
	}
}

func registerCodexFallbackModels(t *testing.T, clientID string) {
	t.Helper()
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(clientID, "codex", []*registry.ModelInfo{
		{ID: "gpt-5.6-sol"},
		{ID: "gpt-5.6-luna"},
		{ID: "gpt-5.6-terra"},
		{ID: "gpt-5.5"},
		{ID: "gpt-5.4"},
		{ID: "gpt-5.4-mini"},
	})
	t.Cleanup(func() { reg.UnregisterClient(clientID) })
}

func setupCodexFallbackManager(t *testing.T, clientID string, errs map[string]error) (*Manager, *codexFallbackTestExecutor) {
	t.Helper()
	registerCodexFallbackModels(t, clientID)
	exec := &codexFallbackTestExecutor{errorsByModel: errs}
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(3, time.Second, 0)
	m.RegisterExecutor(exec)
	if _, err := m.Register(context.Background(), &Auth{ID: clientID, Provider: "codex"}); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	return m, exec
}

func TestCodexContextFallbackChains(t *testing.T) {
	// Hook-first precedence: a Codex context failure diverts straight to the
	// ai-provider failover hook. The legacy same-provider downgrade chain
	// (sol -> 5.5 -> 5.4) no longer runs: downgrading within Codex cannot fit
	// overflow input.
	tests := []struct {
		name   string
		model  string
		errors map[string]error
	}{
		{
			name:   "sol context error diverts without downgrade",
			model:  "gpt-5.6-sol",
			errors: map[string]error{"gpt-5.6-sol": contextTooLargeError(), "gpt-5.5": contextTooLargeError()},
		},
		{
			name:   "luna context error diverts without downgrade",
			model:  "gpt-5.6-luna",
			errors: map[string]error{"gpt-5.6-luna": contextTooLargeError()},
		},
		{
			name:   "terra context error diverts without downgrade",
			model:  "gpt-5.6-terra",
			errors: map[string]error{"gpt-5.6-terra": contextTooLargeError()},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" non-stream", func(t *testing.T) {
			m, exec := setupCodexFallbackManager(t, "codex-fallback-chain-"+tt.model, tt.errors)
			_, err := m.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: tt.model}, cliproxyexecutor.Options{Metadata: map[string]any{}})
			if err == nil {
				t.Fatal("Execute() expected terminal context error, got nil")
			}
			if !errors.Is(err, ErrContextWindowExceeded) {
				t.Fatalf("Execute() error = %v, want errors.Is ErrContextWindowExceeded", err)
			}
			if got, want := exec.ExecuteCalls(), []string{tt.model}; !slicesEqual(got, want) {
				t.Errorf("Execute calls = %v, want %v (no same-provider downgrade after hook divert)", got, want)
			}
		})

		t.Run(tt.name+" stream", func(t *testing.T) {
			m, exec := setupCodexFallbackManager(t, "codex-fallback-chain-stream-"+tt.model, tt.errors)
			result, err := m.ExecuteStream(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: tt.model}, cliproxyexecutor.Options{Metadata: map[string]any{}})
			if err != nil {
				t.Fatalf("ExecuteStream() error = %v, want nil", err)
			}
			if result == nil {
				t.Fatal("ExecuteStream() result = nil, want non-nil")
			}
			terminal := drainStreamResult(t, result)
			if terminal == nil || terminal.Err == nil {
				t.Fatal("expected terminal stream chunk error")
			}
			if !errors.Is(terminal.Err, ErrContextWindowExceeded) {
				t.Errorf("terminal error = %v, want errors.Is ErrContextWindowExceeded", terminal.Err)
			}
			if got, want := exec.StreamCalls(), []string{tt.model}; !slicesEqual(got, want) {
				t.Errorf("ExecuteStream calls = %v, want %v (no same-provider downgrade after hook divert)", got, want)
			}
		})
	}
}

func TestCodexContextFallbackStopsAtGPT54(t *testing.T) {
	// Hook-first precedence: the first context failure is terminal, so the
	// run stops at the requested model instead of walking the downgrade chain.
	errs := map[string]error{
		"gpt-5.6-sol": contextTooLargeError(),
		"gpt-5.5":     contextTooLargeError(),
		"gpt-5.4":     contextTooLargeError(),
	}
	wantCalls := []string{"gpt-5.6-sol"}

	t.Run("non-stream", func(t *testing.T) {
		m, exec := setupCodexFallbackManager(t, "codex-fallback-stop", errs)
		_, err := m.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: "gpt-5.6-sol"}, cliproxyexecutor.Options{Metadata: map[string]any{}})
		if err == nil {
			t.Fatal("Execute() expected error, got nil")
		}
		if !errors.Is(err, ErrContextWindowExceeded) {
			t.Fatalf("Execute() error = %v, want errors.Is ErrContextWindowExceeded", err)
		}
		if got := exec.ExecuteCalls(); !slicesEqual(got, wantCalls) {
			t.Errorf("Execute calls = %v, want %v", got, wantCalls)
		}
	})

	t.Run("stream", func(t *testing.T) {
		m, exec := setupCodexFallbackManager(t, "codex-fallback-stop-stream", errs)
		result, err := m.ExecuteStream(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: "gpt-5.6-sol"}, cliproxyexecutor.Options{Metadata: map[string]any{}})
		if err != nil {
			t.Fatalf("ExecuteStream() error = %v, want nil", err)
		}
		if result == nil {
			t.Fatal("ExecuteStream() result = nil")
		}
		terminal := drainStreamResult(t, result)
		if terminal == nil || terminal.Err == nil {
			t.Fatal("expected terminal stream chunk error")
		}
		if !errors.Is(terminal.Err, ErrContextWindowExceeded) {
			t.Errorf("terminal error = %v, want errors.Is ErrContextWindowExceeded", terminal.Err)
		}
		if got := exec.StreamCalls(); !slicesEqual(got, wantCalls) {
			t.Errorf("ExecuteStream calls = %v, want %v", got, wantCalls)
		}
	})
}

func TestCodexFallbackNonContextErrorRemainsSingleHop(t *testing.T) {
	errs := map[string]error{
		"gpt-5.6-sol": &Error{HTTPStatus: http.StatusBadRequest, Message: "model not supported"},
		"gpt-5.5":     contextTooLargeError(),
	}
	wantCalls := []string{"gpt-5.6-sol", "gpt-5.5"}

	t.Run("non-stream", func(t *testing.T) {
		m, exec := setupCodexFallbackManager(t, "codex-fallback-single-hop", errs)
		_, err := m.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: "gpt-5.6-sol"}, cliproxyexecutor.Options{Metadata: map[string]any{}})
		if err == nil {
			t.Fatal("Execute() expected error, got nil")
		}
		if got := exec.ExecuteCalls(); !slicesEqual(got, wantCalls) {
			t.Errorf("Execute calls = %v, want %v", got, wantCalls)
		}
	})

	t.Run("stream", func(t *testing.T) {
		m, exec := setupCodexFallbackManager(t, "codex-fallback-single-hop-stream", errs)
		result, err := m.ExecuteStream(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: "gpt-5.6-sol"}, cliproxyexecutor.Options{Metadata: map[string]any{}})
		if err != nil {
			t.Fatalf("ExecuteStream() error = %v, want nil", err)
		}
		if result == nil {
			t.Fatal("ExecuteStream() result = nil")
		}
		drainStreamResult(t, result)
		if got := exec.StreamCalls(); !slicesEqual(got, wantCalls) {
			t.Errorf("ExecuteStream calls = %v, want %v", got, wantCalls)
		}
	})
}

func TestBuildCodexFallbackRequestPreservesOriginalDisplayModel(t *testing.T) {
	const clientID = "codex-fallback-display-client"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(clientID, "codex", []*registry.ModelInfo{
		{ID: "gpt-5.6-sol"},
		{ID: "gpt-5.5"},
		{ID: "gpt-5.4"},
	})
	t.Cleanup(func() { reg.UnregisterClient(clientID) })

	req := cliproxyexecutor.Request{Model: "gpt-5.6-sol"}
	opts := cliproxyexecutor.Options{Metadata: map[string]any{}}

	// First hop: sol -> 5.5
	fbReq, fbOpts, ok := buildCodexFallbackRequest(req, opts)
	if !ok {
		t.Fatal("buildCodexFallbackRequest() first hop not ok")
	}
	if fbReq.Model != "gpt-5.5" {
		t.Errorf("first hop model = %q, want %q", fbReq.Model, "gpt-5.5")
	}
	if got := fbOpts.Metadata[cliproxyexecutor.CodexFallbackDisplayModelMetadataKey]; got != "gpt-5.6-sol" {
		t.Errorf("first hop display model = %v, want %v", got, "gpt-5.6-sol")
	}
	if got := fbOpts.Metadata[cliproxyexecutor.RequestedModelMetadataKey]; got != "gpt-5.5" {
		t.Errorf("first hop requested model = %v, want %v", got, "gpt-5.5")
	}

	// Second hop: 5.5 -> 5.4
	fbReq2, fbOpts2, ok := buildCodexFallbackRequest(fbReq, fbOpts)
	if !ok {
		t.Fatal("buildCodexFallbackRequest() second hop not ok")
	}
	if fbReq2.Model != "gpt-5.4" {
		t.Errorf("second hop model = %q, want %q", fbReq2.Model, "gpt-5.4")
	}
	if got := fbOpts2.Metadata[cliproxyexecutor.CodexFallbackDisplayModelMetadataKey]; got != "gpt-5.6-sol" {
		t.Errorf("second hop display model = %v, want %v", got, "gpt-5.6-sol")
	}
	if got := fbOpts2.Metadata[cliproxyexecutor.RequestedModelMetadataKey]; got != "gpt-5.4" {
		t.Errorf("second hop requested model = %v, want %v", got, "gpt-5.4")
	}
}

func TestBuildCodexFallbackRequest(t *testing.T) {
	const clientID = "codex-fallback-test-client"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(clientID, "codex", []*registry.ModelInfo{
		{ID: "gpt-5.4"},
		{ID: "gpt-5.5"},
	})
	t.Cleanup(func() { reg.UnregisterClient(clientID) })
	tests := []struct {
		name             string
		model            string
		wantOK           bool
		wantModel        string
		wantDisplayModel string
	}{
		{
			name:             "gpt-5.5 falls back to gpt-5.4",
			model:            "gpt-5.5",
			wantOK:           true,
			wantModel:        "gpt-5.4",
			wantDisplayModel: "gpt-5.5",
		},
		{
			name:             "gpt-5.5 with suffix falls back preserving suffix",
			model:            "gpt-5.5(high)",
			wantOK:           true,
			wantModel:        "gpt-5.4(high)",
			wantDisplayModel: "gpt-5.5",
		},
		{
			name:   "gpt-5.4 has no fallback",
			model:  "gpt-5.4",
			wantOK: false,
		},
		{
			name:   "unknown model has no fallback",
			model:  "gpt-4o",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := cliproxyexecutor.Request{Model: tt.model}
			opts := cliproxyexecutor.Options{Metadata: map[string]any{}}
			fbReq, fbOpts, ok := buildCodexFallbackRequest(req, opts)
			if ok != tt.wantOK {
				t.Fatalf("buildCodexFallbackRequest() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if fbReq.Model != tt.wantModel {
				t.Errorf("buildCodexFallbackRequest() model = %q, want %q", fbReq.Model, tt.wantModel)
			}
			displayModel := fbOpts.Metadata[cliproxyexecutor.CodexFallbackDisplayModelMetadataKey]
			if displayModel != tt.wantDisplayModel {
				t.Errorf("buildCodexFallbackRequest() display model = %q, want %q", displayModel, tt.wantDisplayModel)
			}
			requestedModel := fbOpts.Metadata[cliproxyexecutor.RequestedModelMetadataKey]
			wantRequestedModel := "gpt-5.4"
			if requestedModel != wantRequestedModel {
				t.Errorf("buildCodexFallbackRequest() requested model = %q, want %q", requestedModel, wantRequestedModel)
			}
		})
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func drainStreamResult(t *testing.T, result *cliproxyexecutor.StreamResult) *cliproxyexecutor.StreamChunk {
	t.Helper()
	var terminal *cliproxyexecutor.StreamChunk
	for chunk := range result.Chunks {
		terminal = &chunk
	}
	return terminal
}
