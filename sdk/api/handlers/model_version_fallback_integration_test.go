package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"github.com/tidwall/gjson"
)

func TestExecuteWithAuthManagerFormats_VersionFallbackGlmFullChain(t *testing.T) {
	const (
		m52      = "glm-5.2"
		m51      = "glm-5.1"
		m5       = "glm-5"
		clientID = "version-fallback-glm-full-chain"
		provider = "openai-compat-glm"
	)
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(clientID, provider, []*registry.ModelInfo{
		{ID: m52}, {ID: m51}, {ID: m5},
	})
	t.Cleanup(func() { reg.UnregisterClient(clientID) })

	var attempts []string
	var mu sync.Mutex
	executor := &interceptorCaptureExecutor{provider: provider}
	executor.execute = func(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
		model := strings.TrimSpace(req.Model)
		mu.Lock()
		attempts = append(attempts, model)
		mu.Unlock()
		if model == m5 {
			return coreexecutor.Response{Payload: []byte(fmt.Sprintf(`{"model":%q,"ok":true}`, m5))}, nil
		}
		return coreexecutor.Response{}, &coreauth.Error{
			HTTPStatus: http.StatusBadRequest,
			Message:    "model not supported",
		}
	}

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	auth := &coreauth.Auth{ID: clientID, Provider: provider, Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register(): %v", err)
	}

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	handler.SetQuotaExceededBehavior(internalconfig.QuotaExceeded{SwitchPreviewModel: true})

	body, _, errMsg := handler.executeWithAuthManagerFormats(
		context.Background(), "openai", "openai", m52,
		[]byte(fmt.Sprintf(`{"model":%q}`, m52)), "", false, modelExecutionOptions{},
	)
	if errMsg != nil {
		t.Fatalf("execute err = %+v, attempts=%v", errMsg, attempts)
	}
	if gjson.GetBytes(body, "model").String() != m52 {
		t.Fatalf("response model = %s, want %s", gjson.GetBytes(body, "model").String(), m52)
	}
	mu.Lock()
	defer mu.Unlock()
	wantAttempts := []string{m52, m51, m5}
	if len(attempts) != len(wantAttempts) {
		t.Fatalf("attempts = %v, want %v", attempts, wantAttempts)
	}
	for i := range wantAttempts {
		if attempts[i] != wantAttempts[i] {
			t.Fatalf("attempts[%d] = %q, want %q (full chain)", i, attempts[i], wantAttempts[i])
		}
	}
}

func TestExecuteWithAuthManagerFormats_VersionFallbackKimiChain(t *testing.T) {
	const (
		high     = "kimi-k2.7"
		low      = "kimi-k2.6"
		lower    = "kimi-k2.5"
		clientID = "version-fallback-kimi-chain"
		provider = "openai-compat-kimi"
	)
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(clientID, provider, []*registry.ModelInfo{
		{ID: high}, {ID: low}, {ID: lower},
	})
	t.Cleanup(func() { reg.UnregisterClient(clientID) })

	var attempts []string
	executor := &interceptorCaptureExecutor{provider: provider}
	executor.execute = func(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
		model := strings.TrimSpace(req.Model)
		attempts = append(attempts, model)
		if model == lower {
			return coreexecutor.Response{Payload: []byte(fmt.Sprintf(`{"model":%q}`, lower))}, nil
		}
		return coreexecutor.Response{}, &coreauth.Error{HTTPStatus: http.StatusBadRequest, Message: "unsupported model"}
	}

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	auth := &coreauth.Auth{ID: clientID, Provider: provider, Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register(): %v", err)
	}
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	handler.SetQuotaExceededBehavior(internalconfig.QuotaExceeded{SwitchPreviewModel: true})

	_, _, errMsg := handler.executeWithAuthManagerFormats(
		context.Background(), "openai", "openai", high,
		[]byte(fmt.Sprintf(`{"model":%q}`, high)), "", false, modelExecutionOptions{},
	)
	if errMsg != nil {
		t.Fatalf("execute err = %+v attempts=%v", errMsg, attempts)
	}
	want := []string{high, low, lower}
	if len(attempts) != len(want) {
		t.Fatalf("attempts = %v want %v", attempts, want)
	}
	for i := range want {
		if attempts[i] != want[i] {
			t.Fatalf("attempts[%d]=%q want %q", i, attempts[i], want[i])
		}
	}
}

func TestExecuteWithAuthManagerFormats_VersionFallbackMinimaxChain(t *testing.T) {
	const (
		high     = "minimax-m3"
		low      = "minimax-m2.7"
		clientID = "version-fallback-minimax-chain"
		provider = "openai-compat-minimax"
	)
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(clientID, provider, []*registry.ModelInfo{{ID: high}, {ID: low}})
	t.Cleanup(func() { reg.UnregisterClient(clientID) })

	var attempts []string
	executor := &interceptorCaptureExecutor{provider: provider}
	executor.execute = func(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
		model := strings.TrimSpace(req.Model)
		attempts = append(attempts, model)
		if model == low {
			return coreexecutor.Response{Payload: []byte(fmt.Sprintf(`{"model":%q}`, low))}, nil
		}
		return coreexecutor.Response{}, &coreauth.Error{HTTPStatus: http.StatusBadRequest, Message: "unsupported model"}
	}

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	auth := &coreauth.Auth{ID: clientID, Provider: provider, Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register(): %v", err)
	}
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	handler.SetQuotaExceededBehavior(internalconfig.QuotaExceeded{SwitchPreviewModel: true})

	_, _, errMsg := handler.executeWithAuthManagerFormats(
		context.Background(), "openai", "openai", high,
		[]byte(fmt.Sprintf(`{"model":%q}`, high)), "", false, modelExecutionOptions{},
	)
	if errMsg != nil {
		t.Fatalf("execute err = %+v attempts=%v", errMsg, attempts)
	}
	want := []string{high, low}
	if len(attempts) != len(want) {
		t.Fatalf("attempts = %v want %v", attempts, want)
	}
}

// TestExecuteStreamWithAuthManagerFormats_VersionFallbackFirstHopSuccess proves
// the fast path: when the first hop succeeds, the handler must not consult
// modelversion.Chain, must not spawn the chunk-rewrite wrapper goroutine, and
// must hand back the executor's own dataChan/errChan untouched — full payload,
// clean errChan, same model as requested (no restore rewrite needed).
func TestExecuteStreamWithAuthManagerFormats_VersionFallbackFirstHopSuccess(t *testing.T) {
	const (
		model    = "glm-5.2-stream-first-hop"
		clientID = "version-fallback-stream-first-hop"
		provider = "openai-compat-glm-stream"
	)
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(clientID, provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient(clientID) })

	var attempts int
	executor := &interceptorCaptureExecutor{provider: provider}
	executor.stream = func(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (*coreexecutor.StreamResult, error) {
		attempts++
		chunks := make(chan coreexecutor.StreamChunk, 2)
		chunks <- coreexecutor.StreamChunk{Payload: []byte(fmt.Sprintf(`{"model":%q,"chunk":1}`, model))}
		chunks <- coreexecutor.StreamChunk{Payload: []byte(fmt.Sprintf(`{"model":%q,"chunk":2}`, model))}
		close(chunks)
		return &coreexecutor.StreamResult{Chunks: chunks}, nil
	}

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	auth := &coreauth.Auth{ID: clientID, Provider: provider, Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register(): %v", err)
	}

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	handler.SetQuotaExceededBehavior(internalconfig.QuotaExceeded{SwitchPreviewModel: true})

	dataChan, _, errChan := handler.executeStreamWithAuthManagerFormats(
		context.Background(), "openai", "openai", model,
		[]byte(fmt.Sprintf(`{"model":%q}`, model)), "", false, modelExecutionOptions{},
	)
	if dataChan == nil {
		t.Fatalf("dataChan = nil, want non-nil on first-hop success")
	}

	var got []byte
	for chunk := range dataChan {
		got = append(got, chunk...)
	}
	for msg := range errChan {
		if msg != nil {
			t.Fatalf("errChan emitted %+v, want clean close on first-hop success", msg)
		}
	}

	want := fmt.Sprintf(`{"model":%q,"chunk":1}`, model) + fmt.Sprintf(`{"model":%q,"chunk":2}`, model)
	if string(got) != want {
		t.Fatalf("payload = %q, want %q (full payload, no rewrite)", string(got), want)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no fallback hop on first-hop success)", attempts)
	}
}
