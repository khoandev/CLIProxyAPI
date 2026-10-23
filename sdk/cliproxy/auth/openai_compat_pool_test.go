package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

const openAICompatPoolProviderKey = "openai-compatible-pool"

type openAICompatPoolExecutor struct {
	id string

	mu                sync.Mutex
	executeModels     []string
	countModels       []string
	streamModels      []string
	executePayloads   map[string][]byte
	executeErrors     map[string]error
	countErrors       map[string]error
	streamFirstErrors map[string]error
	streamAttempts    map[string]bool
	streamPayloads    map[string][]cliproxyexecutor.StreamChunk
}

func (e *openAICompatPoolExecutor) Identifier() string { return e.id }

func (e *openAICompatPoolExecutor) Execute(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	_ = ctx
	_ = auth
	_ = opts
	e.mu.Lock()
	e.executeModels = append(e.executeModels, req.Model)
	payload := append([]byte(nil), e.executePayloads[req.Model]...)
	err := e.executeErrors[req.Model]
	e.mu.Unlock()
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	if len(payload) > 0 {
		return cliproxyexecutor.Response{Payload: payload}, nil
	}
	return cliproxyexecutor.Response{Payload: []byte(req.Model)}, nil
}

func (e *openAICompatPoolExecutor) ExecuteStream(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	_ = auth
	_ = opts
	e.mu.Lock()
	e.streamModels = append(e.streamModels, req.Model)
	err := e.streamFirstErrors[req.Model]
	attempted := e.streamAttempts[req.Model]
	payloadChunks, hasCustomChunks := e.streamPayloads[req.Model]
	chunks := append([]cliproxyexecutor.StreamChunk(nil), payloadChunks...)
	e.mu.Unlock()
	if attempted {
		cliproxyexecutor.MarkUpstreamAttempt(ctx)
	}
	ch := make(chan cliproxyexecutor.StreamChunk, max(1, len(chunks)))
	if err != nil {
		ch <- cliproxyexecutor.StreamChunk{Err: err}
		close(ch)
		return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Model": {req.Model}}, Chunks: ch}, nil
	}
	if !hasCustomChunks {
		ch <- cliproxyexecutor.StreamChunk{Payload: []byte(req.Model)}
	} else {
		for _, chunk := range chunks {
			ch <- chunk
		}
	}
	close(ch)
	return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Model": {req.Model}}, Chunks: ch}, nil
}

func (e *openAICompatPoolExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *openAICompatPoolExecutor) CountTokens(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	_ = ctx
	_ = auth
	_ = opts
	e.mu.Lock()
	e.countModels = append(e.countModels, req.Model)
	err := e.countErrors[req.Model]
	e.mu.Unlock()
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	return cliproxyexecutor.Response{Payload: []byte(req.Model)}, nil
}

func (e *openAICompatPoolExecutor) HttpRequest(ctx context.Context, auth *Auth, req *http.Request) (*http.Response, error) {
	_ = ctx
	_ = auth
	_ = req
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "HttpRequest not implemented"}
}

func (e *openAICompatPoolExecutor) ExecuteModels() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.executeModels))
	copy(out, e.executeModels)
	return out
}

func (e *openAICompatPoolExecutor) CountModels() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.countModels))
	copy(out, e.countModels)
	return out
}

func (e *openAICompatPoolExecutor) StreamModels() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.streamModels))
	copy(out, e.streamModels)
	return out
}

type authScopedOpenAICompatPoolExecutor struct {
	id string

	mu            sync.Mutex
	executeCalls  []string
	streamCalls   []string
	executeErrors map[string]error
	streamErrors  map[string]error
}

func (e *authScopedOpenAICompatPoolExecutor) Identifier() string { return e.id }

func (e *authScopedOpenAICompatPoolExecutor) Execute(_ context.Context, auth *Auth, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	call := auth.ID + "|" + req.Model
	e.mu.Lock()
	e.executeCalls = append(e.executeCalls, call)
	err := e.executeErrors[auth.ID]
	e.mu.Unlock()
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	return cliproxyexecutor.Response{Payload: []byte(call)}, nil
}

func (e *authScopedOpenAICompatPoolExecutor) ExecuteStream(_ context.Context, auth *Auth, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	call := auth.ID + "|" + req.Model
	e.mu.Lock()
	e.streamCalls = append(e.streamCalls, call)
	err := e.streamErrors[auth.ID]
	e.mu.Unlock()
	ch := make(chan cliproxyexecutor.StreamChunk, 1)
	if err != nil {
		ch <- cliproxyexecutor.StreamChunk{Err: err}
	} else {
		ch <- cliproxyexecutor.StreamChunk{Payload: []byte(call)}
	}
	close(ch)
	return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Auth": {auth.ID}}, Chunks: ch}, nil
}

func (e *authScopedOpenAICompatPoolExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *authScopedOpenAICompatPoolExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusNotImplemented, Message: "CountTokens not implemented"}
}

func (e *authScopedOpenAICompatPoolExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "HttpRequest not implemented"}
}

func (e *authScopedOpenAICompatPoolExecutor) ExecuteCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.executeCalls))
	copy(out, e.executeCalls)
	return out
}

func (e *authScopedOpenAICompatPoolExecutor) StreamCalls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.streamCalls))
	copy(out, e.streamCalls)
	return out
}

func newOpenAICompatPoolTestManager(t *testing.T, alias string, models []internalconfig.OpenAICompatibilityModel, executor *openAICompatPoolExecutor) *Manager {
	t.Helper()
	cfg := &internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{{
			Name:   "pool",
			Models: models,
		}},
	}
	m := NewManager(nil, nil, nil)
	m.SetConfig(cfg)
	if executor == nil {
		executor = &openAICompatPoolExecutor{id: openAICompatPoolProviderKey}
	}
	m.RegisterExecutor(executor)

	auth := &Auth{
		ID:       "pool-auth-" + t.Name(),
		Provider: openAICompatPoolProviderKey,
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "test-key",
			"compat_name":  "pool",
			"provider_key": openAICompatPoolProviderKey,
		},
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, openAICompatPoolProviderKey, []*registry.ModelInfo{{ID: alias}})
	t.Cleanup(func() {
		reg.UnregisterClient(auth.ID)
	})
	return m
}

func readOpenAICompatStreamPayload(t *testing.T, streamResult *cliproxyexecutor.StreamResult) string {
	t.Helper()
	if streamResult == nil {
		t.Fatal("expected stream result")
	}
	var payload []byte
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		payload = append(payload, chunk.Payload...)
	}
	return string(payload)
}

func TestManagerExecuteCount_OpenAICompatAliasPoolStopsOnInvalidRequest(t *testing.T) {
	alias := "claude-opus-4.66"
	invalidErr := &Error{HTTPStatus: http.StatusUnprocessableEntity, Message: "unprocessable entity"}
	executor := &openAICompatPoolExecutor{
		id:          openAICompatPoolProviderKey,
		countErrors: map[string]error{"deepseek-v3.1": invalidErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	_, err := m.ExecuteCount(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err == nil || err.Error() != invalidErr.Error() {
		t.Fatalf("execute count error = %v, want %v", err, invalidErr)
	}
	got := executor.CountModels()
	if len(got) != 1 || got[0] != "deepseek-v3.1" {
		t.Fatalf("count calls = %v, want only first invalid model", got)
	}
}
func TestResolveModelAliasPoolFromConfigModels(t *testing.T) {
	models := []modelAliasEntry{
		internalconfig.OpenAICompatibilityModel{Name: "deepseek-v3.1", Alias: "claude-opus-4.66"},
		internalconfig.OpenAICompatibilityModel{Name: "glm-5", Alias: "claude-opus-4.66"},
		internalconfig.OpenAICompatibilityModel{Name: "kimi-k2.5", Alias: "claude-opus-4.66"},
	}
	got := resolveModelAliasPoolFromConfigModels("claude-opus-4.66(8192)", models)
	want := []string{"deepseek-v3.1(8192)", "glm-5(8192)", "kimi-k2.5(8192)"}
	if len(got) != len(want) {
		t.Fatalf("pool len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pool[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
func TestManagerExecute_OpenAICompatAliasPreservesColonInUpstreamModel(t *testing.T) {
	alias := "deepseek-v4-pro"
	executor := &openAICompatPoolExecutor{id: openAICompatPoolProviderKey}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v4-flash:max", Alias: alias},
	}, executor)

	if _, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := executor.ExecuteModels()
	if len(got) != 1 || got[0] != "deepseek-v4-flash:max" {
		t.Fatalf("execute models = %v, want [deepseek-v4-flash:max]", got)
	}
}
func TestManagerExecute_OpenAICompatAliasResolvesColonEffort(t *testing.T) {
	alias := "deepseek-v4-pro"
	executor := &openAICompatPoolExecutor{id: openAICompatPoolProviderKey}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{
			Name:  "deepseek-v4-flash",
			Alias: alias,
			Thinking: &registry.ThinkingSupport{
				Levels: []string{"low", "medium", "high", "xhigh"},
			},
		},
	}, executor)

	if _, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias + ":xhigh"}, cliproxyexecutor.Options{}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := executor.ExecuteModels()
	if len(got) != 1 || got[0] != "deepseek-v4-flash:xhigh" {
		t.Fatalf("execute models = %v, want [deepseek-v4-flash:xhigh]", got)
	}
}

func TestResolveModelAliasPoolPrefersExactSuffixedAlias(t *testing.T) {
	models := []modelAliasEntry{
		internalconfig.OpenAICompatibilityModel{Name: "base-model", Alias: "public"},
		internalconfig.OpenAICompatibilityModel{Name: "low-model", Alias: "public(low)", ForceMapping: boolPtr(true)},
	}
	got := resolveModelAliasPoolFromConfigModels("public(low)", models)
	if len(got) != 1 || got[0] != "low-model(low)" {
		t.Fatalf("exact suffixed pool = %v, want [low-model(low)]", got)
	}
	result := resolveModelAliasResultFromConfigModels("public(low)", models)
	if result.UpstreamModel != "low-model(low)" || !result.ForceMapping {
		t.Fatalf("exact suffixed alias result = %+v, want low-model(low) with force mapping", result)
	}
}

func TestManagerExecute_OpenAICompatAliasPoolRotatesWithinAuth(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{id: openAICompatPoolProviderKey}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	for i := 0; i < 3; i++ {
		resp, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
		if len(resp.Payload) == 0 {
			t.Fatalf("execute %d returned empty payload", i)
		}
	}

	got := executor.ExecuteModels()
	want := []string{"deepseek-v3.1", "glm-5", "deepseek-v3.1"}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecute_OpenAICompatAliasPoolForceMappingRotatesAndRewritesResponse(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{
		id: openAICompatPoolProviderKey,
		executePayloads: map[string][]byte{
			"deepseek-v3.1": []byte(`{"model":"deepseek-v3.1"}`),
			"glm-5":         []byte(`{"model":"glm-5"}`),
		},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias, ForceMapping: boolPtr(true)},
		{Name: "glm-5", Alias: alias, ForceMapping: boolPtr(true)},
	}, executor)

	var payloads []string
	for i := 0; i < 2; i++ {
		resp, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
		payloads = append(payloads, string(resp.Payload))
	}

	got := executor.ExecuteModels()
	wantModels := []string{"deepseek-v3.1", "glm-5"}
	for i := range wantModels {
		if got[i] != wantModels[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], wantModels[i])
		}
	}
	wantPayloads := []string{`{"model":"claude-opus-4.66"}`, `{"model":"claude-opus-4.66"}`}
	for i := range wantPayloads {
		if payloads[i] != wantPayloads[i] {
			t.Fatalf("payload %d = %s, want %s", i, payloads[i], wantPayloads[i])
		}
	}
}

func TestManagerExecute_OpenAICompatAliasPoolStopsOnBadRequest(t *testing.T) {
	alias := "claude-opus-4.66"
	invalidErr := &Error{HTTPStatus: http.StatusBadRequest, Message: "invalid_request_error: malformed payload"}
	executor := &openAICompatPoolExecutor{
		id:            openAICompatPoolProviderKey,
		executeErrors: map[string]error{"deepseek-v3.1": invalidErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	_, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err == nil || err.Error() != invalidErr.Error() {
		t.Fatalf("execute error = %v, want %v", err, invalidErr)
	}
	got := executor.ExecuteModels()
	if len(got) != 1 || got[0] != "deepseek-v3.1" {
		t.Fatalf("execute calls = %v, want only first invalid model", got)
	}
}

func TestManagerExecute_OpenAICompatAliasPoolFallsBackOnModelSupportBadRequest(t *testing.T) {
	alias := "claude-opus-4.66"
	modelSupportErr := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    "invalid_request_error: The requested model is not supported.",
	}
	executor := &openAICompatPoolExecutor{
		id:            openAICompatPoolProviderKey,
		executeErrors: map[string]error{"deepseek-v3.1": modelSupportErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	resp, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute error = %v, want fallback success", err)
	}
	if string(resp.Payload) != "glm-5" {
		t.Fatalf("payload = %q, want %q", string(resp.Payload), "glm-5")
	}
	got := executor.ExecuteModels()
	want := []string{"deepseek-v3.1", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], want[i])
		}
	}

	updated, ok := m.GetByID("pool-auth-" + t.Name())
	if !ok || updated == nil {
		t.Fatalf("expected auth to remain registered")
	}
	state := updated.ModelStates["deepseek-v3.1"]
	if state == nil {
		t.Fatalf("expected suspended upstream model state")
	}
	if !state.Unavailable || state.NextRetryAfter.IsZero() {
		t.Fatalf("expected upstream model suspension, got %+v", state)
	}
}

func TestManagerExecute_OpenAICompatAliasPoolFallsBackOnModelSupportUnprocessableEntity(t *testing.T) {
	alias := "claude-opus-4.66"
	modelSupportErr := &Error{
		HTTPStatus: http.StatusUnprocessableEntity,
		Message:    "The requested model is not supported.",
	}
	executor := &openAICompatPoolExecutor{
		id:            openAICompatPoolProviderKey,
		executeErrors: map[string]error{"deepseek-v3.1": modelSupportErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	resp, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute error = %v, want fallback success", err)
	}
	if string(resp.Payload) != "glm-5" {
		t.Fatalf("payload = %q, want %q", string(resp.Payload), "glm-5")
	}
	got := executor.ExecuteModels()
	want := []string{"deepseek-v3.1", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecute_OpenAICompatAliasPoolFallsBackWithinSameAuth(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{
		id:            openAICompatPoolProviderKey,
		executeErrors: map[string]error{"deepseek-v3.1": &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota"}},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	resp, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if string(resp.Payload) != "glm-5" {
		t.Fatalf("payload = %q, want %q", string(resp.Payload), "glm-5")
	}
	got := executor.ExecuteModels()
	want := []string{"deepseek-v3.1", "glm-5"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecute_OpenAICompatAliasPoolUsesSelectedModelForceMapping(t *testing.T) {
	alias := "public-model"
	executor := &openAICompatPoolExecutor{
		id:              openAICompatPoolProviderKey,
		executeErrors:   map[string]error{"first-upstream": &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota"}},
		executePayloads: map[string][]byte{"second-upstream": []byte(`{"model":"second-upstream"}`)},
	}
	manager := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "first-upstream", Alias: alias, ForceMapping: boolPtr(true)},
		{Name: "second-upstream", Alias: alias, ForceMapping: boolPtr(false)},
	}, executor)

	response, errExecute := manager.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}
	if got := string(response.Payload); got != `{"model":"second-upstream"}` {
		t.Fatalf("payload = %s, want selected model without force mapping", got)
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolRetriesOnEmptyBootstrap(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{
		id: openAICompatPoolProviderKey,
		streamPayloads: map[string][]cliproxyexecutor.StreamChunk{
			"deepseek-v3.1": {},
		},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias, ForceMapping: boolPtr(false)},
		{Name: "glm-5", Alias: alias, ForceMapping: boolPtr(false)},
	}, executor)

	streamResult, err := m.ExecuteStream(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute stream: %v", err)
	}
	var payload []byte
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		payload = append(payload, chunk.Payload...)
	}
	if string(payload) != "glm-5" {
		t.Fatalf("payload = %q, want %q", string(payload), "glm-5")
	}
	got := executor.StreamModels()
	want := []string{"deepseek-v3.1", "glm-5"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stream call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolFallsBackBeforeFirstByte(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{
		id:                openAICompatPoolProviderKey,
		streamFirstErrors: map[string]error{"deepseek-v3.1": &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota"}},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias, ForceMapping: boolPtr(false)},
		{Name: "glm-5", Alias: alias, ForceMapping: boolPtr(false)},
	}, executor)

	streamResult, err := m.ExecuteStream(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute stream: %v", err)
	}
	var payload []byte
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		payload = append(payload, chunk.Payload...)
	}
	if string(payload) != "glm-5" {
		t.Fatalf("payload = %q, want %q", string(payload), "glm-5")
	}
	got := executor.StreamModels()
	want := []string{"deepseek-v3.1", "glm-5"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stream call %d model = %q, want %q", i, got[i], want[i])
		}
	}
	if gotHeader := streamResult.Headers.Get("X-Model"); gotHeader != "glm-5" {
		t.Fatalf("header X-Model = %q, want %q", gotHeader, "glm-5")
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolStopsOnInvalidRequest(t *testing.T) {
	alias := "claude-opus-4.66"
	invalidErr := &Error{HTTPStatus: http.StatusUnprocessableEntity, Message: "unprocessable entity"}
	executor := &openAICompatPoolExecutor{
		id:                openAICompatPoolProviderKey,
		streamFirstErrors: map[string]error{"deepseek-v3.1": invalidErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	_, err := m.ExecuteStream(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err == nil || err.Error() != invalidErr.Error() {
		t.Fatalf("execute stream error = %v, want %v", err, invalidErr)
	}
	got := executor.StreamModels()
	if len(got) != 1 || got[0] != "deepseek-v3.1" {
		t.Fatalf("stream calls = %v, want only first invalid model", got)
	}
}

func TestManagerExecute_OpenAICompatAliasPoolSkipsSuspendedUpstreamOnLaterRequests(t *testing.T) {
	alias := "claude-opus-4.66"
	modelSupportErr := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    "invalid_request_error: The requested model is not supported.",
	}
	executor := &openAICompatPoolExecutor{
		id:            openAICompatPoolProviderKey,
		executeErrors: map[string]error{"deepseek-v3.1": modelSupportErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	for i := 0; i < 3; i++ {
		resp, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
		if string(resp.Payload) != "glm-5" {
			t.Fatalf("execute %d payload = %q, want %q", i, string(resp.Payload), "glm-5")
		}
	}

	got := executor.ExecuteModels()
	want := []string{"deepseek-v3.1", "glm-5", "glm-5", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("execute calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("execute call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolSkipsSuspendedUpstreamOnLaterRequests(t *testing.T) {
	alias := "claude-opus-4.66"
	modelSupportErr := &Error{
		HTTPStatus: http.StatusUnprocessableEntity,
		Message:    "The requested model is not supported.",
	}
	executor := &openAICompatPoolExecutor{
		id:                openAICompatPoolProviderKey,
		streamFirstErrors: map[string]error{"deepseek-v3.1": modelSupportErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias, ForceMapping: boolPtr(false)},
		{Name: "glm-5", Alias: alias, ForceMapping: boolPtr(false)},
	}, executor)

	for i := 0; i < 3; i++ {
		streamResult, err := m.ExecuteStream(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute stream %d: %v", i, err)
		}
		if payload := readOpenAICompatStreamPayload(t, streamResult); payload != "glm-5" {
			t.Fatalf("execute stream %d payload = %q, want %q", i, payload, "glm-5")
		}
		if gotHeader := streamResult.Headers.Get("X-Model"); gotHeader != "glm-5" {
			t.Fatalf("execute stream %d header X-Model = %q, want %q", i, gotHeader, "glm-5")
		}
	}

	got := executor.StreamModels()
	want := []string{"deepseek-v3.1", "glm-5", "glm-5", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("stream calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stream call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecuteCount_OpenAICompatAliasPoolRotatesWithinAuth(t *testing.T) {
	alias := "claude-opus-4.66"
	executor := &openAICompatPoolExecutor{id: openAICompatPoolProviderKey}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	for i := 0; i < 2; i++ {
		resp, err := m.ExecuteCount(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute count %d: %v", i, err)
		}
		if len(resp.Payload) == 0 {
			t.Fatalf("execute count %d returned empty payload", i)
		}
	}

	got := executor.CountModels()
	want := []string{"deepseek-v3.1", "glm-5"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("count call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecuteCount_OpenAICompatAliasPoolSkipsSuspendedUpstreamOnLaterRequests(t *testing.T) {
	alias := "claude-opus-4.66"
	modelSupportErr := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    "invalid_request_error: The requested model is unsupported.",
	}
	executor := &openAICompatPoolExecutor{
		id:          openAICompatPoolProviderKey,
		countErrors: map[string]error{"deepseek-v3.1": modelSupportErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	for i := 0; i < 3; i++ {
		resp, err := m.ExecuteCount(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
		if err != nil {
			t.Fatalf("execute count %d: %v", i, err)
		}
		if string(resp.Payload) != "glm-5" {
			t.Fatalf("execute count %d payload = %q, want %q", i, string(resp.Payload), "glm-5")
		}
	}

	got := executor.CountModels()
	want := []string{"deepseek-v3.1", "glm-5", "glm-5", "glm-5"}
	if len(got) != len(want) {
		t.Fatalf("count calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("count call %d model = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestManagerExecute_OpenAICompatAliasPoolBlockedAuthDoesNotConsumeRetryBudget(t *testing.T) {
	alias := "claude-opus-4.66"
	cfg := &internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{{
			Name: "pool",
			Models: []internalconfig.OpenAICompatibilityModel{
				{Name: "deepseek-v3.1", Alias: alias},
				{Name: "glm-5", Alias: alias},
			},
		}},
	}
	m := NewManager(nil, nil, nil)
	m.SetConfig(cfg)
	m.SetRetryConfig(0, 0, 1)

	executor := &authScopedOpenAICompatPoolExecutor{id: openAICompatPoolProviderKey}
	m.RegisterExecutor(executor)

	badAuth := &Auth{
		ID:       "aa-blocked-auth",
		Provider: openAICompatPoolProviderKey,
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "bad-key",
			"compat_name":  "pool",
			"provider_key": openAICompatPoolProviderKey,
		},
	}
	goodAuth := &Auth{
		ID:       "bb-good-auth",
		Provider: openAICompatPoolProviderKey,
		Status:   StatusActive,
		Attributes: map[string]string{
			"api_key":      "good-key",
			"compat_name":  "pool",
			"provider_key": openAICompatPoolProviderKey,
		},
	}
	if _, err := m.Register(context.Background(), badAuth); err != nil {
		t.Fatalf("register bad auth: %v", err)
	}
	if _, err := m.Register(context.Background(), goodAuth); err != nil {
		t.Fatalf("register good auth: %v", err)
	}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(badAuth.ID, openAICompatPoolProviderKey, []*registry.ModelInfo{{ID: alias}})
	reg.RegisterClient(goodAuth.ID, openAICompatPoolProviderKey, []*registry.ModelInfo{{ID: alias}})
	t.Cleanup(func() {
		reg.UnregisterClient(badAuth.ID)
		reg.UnregisterClient(goodAuth.ID)
	})

	modelSupportErr := &Error{
		HTTPStatus: http.StatusBadRequest,
		Message:    "invalid_request_error: The requested model is not supported.",
	}
	for _, upstreamModel := range []string{"deepseek-v3.1", "glm-5"} {
		m.MarkResult(context.Background(), Result{
			AuthID:   badAuth.ID,
			Provider: openAICompatPoolProviderKey,
			Model:    upstreamModel,
			Success:  false,
			Error:    modelSupportErr,
		})
	}

	resp, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute error = %v, want success via fallback auth", err)
	}
	if !strings.HasPrefix(string(resp.Payload), goodAuth.ID+"|") {
		t.Fatalf("payload = %q, want auth %q", string(resp.Payload), goodAuth.ID)
	}

	got := executor.ExecuteCalls()
	if len(got) != 1 {
		t.Fatalf("execute calls = %v, want only one real execution on fallback auth", got)
	}
	if !strings.HasPrefix(got[0], goodAuth.ID+"|") {
		t.Fatalf("execute call = %q, want fallback auth %q", got[0], goodAuth.ID)
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolStopsOnInvalidBootstrap(t *testing.T) {
	alias := "claude-opus-4.66"
	invalidErr := &Error{HTTPStatus: http.StatusBadRequest, Message: "invalid_request_error: malformed payload"}
	executor := &openAICompatPoolExecutor{
		id:                openAICompatPoolProviderKey,
		streamFirstErrors: map[string]error{"deepseek-v3.1": invalidErr},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	streamResult, err := m.ExecuteStream(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if err == nil {
		t.Fatal("expected invalid request error")
	}
	if err != invalidErr {
		t.Fatalf("error = %v, want %v", err, invalidErr)
	}
	if streamResult != nil {
		t.Fatalf("streamResult = %#v, want nil on invalid bootstrap", streamResult)
	}
	if got := executor.StreamModels(); len(got) != 1 || got[0] != "deepseek-v3.1" {
		t.Fatalf("stream calls = %v, want only first upstream model", got)
	}
}
func newTwoAuthOpenAICompatPoolManager(t *testing.T, executor *authScopedOpenAICompatPoolExecutor) (*Manager, *Auth, *Auth) {
	t.Helper()
	alias := "claude-opus-4.66"
	m := NewManager(nil, nil, nil)
	m.SetConfig(&internalconfig.Config{
		OpenAICompatibility: []internalconfig.OpenAICompatibility{{
			Name:   "pool",
			Models: []internalconfig.OpenAICompatibilityModel{{Name: "deepseek-v3.1", Alias: alias}},
		}},
	})
	m.SetRetryConfig(0, 0, 1)
	m.RegisterExecutor(executor)

	badAuth := &Auth{ID: "aa-429-" + t.Name(), Provider: openAICompatPoolProviderKey, Status: StatusActive, Attributes: map[string]string{
		"api_key": "bad-key", "compat_name": "pool", "provider_key": openAICompatPoolProviderKey,
	}}
	goodAuth := &Auth{ID: "bb-good-" + t.Name(), Provider: openAICompatPoolProviderKey, Status: StatusActive, Attributes: map[string]string{
		"api_key": "good-key", "compat_name": "pool", "provider_key": openAICompatPoolProviderKey,
	}}
	for _, auth := range []*Auth{badAuth, goodAuth} {
		if _, err := m.Register(context.Background(), auth); err != nil {
			t.Fatalf("register auth %s: %v", auth.ID, err)
		}
	}
	reg := registry.GetGlobalRegistry()
	for _, auth := range []*Auth{badAuth, goodAuth} {
		reg.RegisterClient(auth.ID, openAICompatPoolProviderKey, []*registry.ModelInfo{{ID: alias}})
		authID := auth.ID
		t.Cleanup(func() { reg.UnregisterClient(authID) })
	}
	return m, badAuth, goodAuth
}

func TestManagerExecute_OpenAICompat429RotatesPastCredentialLimit(t *testing.T) {
	executor := &authScopedOpenAICompatPoolExecutor{id: openAICompatPoolProviderKey, executeErrors: map[string]error{}}
	m, badAuth, goodAuth := newTwoAuthOpenAICompatPoolManager(t, executor)
	executor.executeErrors[badAuth.ID] = &Error{HTTPStatus: http.StatusTooManyRequests, Message: "bad key rate limited"}

	resp, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: "claude-opus-4.66"}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute error = %v, want fallback success", err)
	}
	if !strings.HasPrefix(string(resp.Payload), goodAuth.ID+"|") {
		t.Fatalf("payload = %q, want auth %q", string(resp.Payload), goodAuth.ID)
	}
	got := executor.ExecuteCalls()
	if len(got) != 2 || !strings.HasPrefix(got[0], badAuth.ID+"|") || !strings.HasPrefix(got[1], goodAuth.ID+"|") {
		t.Fatalf("execute calls = %v, want one call per auth in order", got)
	}
}

func TestManagerExecuteStream_OpenAICompat429RotatesPastCredentialLimit(t *testing.T) {
	executor := &authScopedOpenAICompatPoolExecutor{id: openAICompatPoolProviderKey, streamErrors: map[string]error{}}
	m, badAuth, goodAuth := newTwoAuthOpenAICompatPoolManager(t, executor)
	executor.streamErrors[badAuth.ID] = &Error{HTTPStatus: http.StatusTooManyRequests, Message: "bad key rate limited"}

	streamResult, err := m.ExecuteStream(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: "claude-opus-4.66"}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("execute stream error = %v, want fallback success", err)
	}
	if got := readOpenAICompatStreamPayload(t, streamResult); !strings.HasPrefix(got, goodAuth.ID+"|") {
		t.Fatalf("stream payload = %q, want auth %q", got, goodAuth.ID)
	}
	got := executor.StreamCalls()
	if len(got) != 2 || !strings.HasPrefix(got[0], badAuth.ID+"|") || !strings.HasPrefix(got[1], goodAuth.ID+"|") {
		t.Fatalf("stream calls = %v, want one call per auth in order", got)
	}
}

func TestManagerExecute_OpenAICompat429AllKeysReturnsFinalErrorWithoutWaiting(t *testing.T) {
	executor := &authScopedOpenAICompatPoolExecutor{id: openAICompatPoolProviderKey, executeErrors: map[string]error{}}
	m, badAuth, goodAuth := newTwoAuthOpenAICompatPoolManager(t, executor)
	m.SetRetryConfig(1, 30*time.Second, 1)
	badErr := &retryAfterStatusError{status: http.StatusTooManyRequests, message: "bad key rate limited", retryAfter: 10 * time.Second}
	goodErr := &retryAfterStatusError{status: http.StatusTooManyRequests, message: "good key rate limited", retryAfter: 10 * time.Second}
	executor.executeErrors[badAuth.ID] = badErr
	executor.executeErrors[goodAuth.ID] = goodErr

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := m.Execute(ctx, []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: "claude-opus-4.66"}, cliproxyexecutor.Options{})
	if err == nil {
		t.Fatal("expected final upstream rate-limit error")
	}
	if err == context.DeadlineExceeded || strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("error = %v, want final upstream error without cooldown wait", err)
	}
	// The error must preserve the 429 status but the message is sanitized to avoid
	// leaking raw provider details after all relay channels are exhausted.
	if statusCodeFromError(err) != http.StatusTooManyRequests {
		t.Fatalf("error = %v (status=%d), want 429 rate-limit error", err, statusCodeFromError(err))
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("execute elapsed = %v, want no request-level cooldown sleep", elapsed)
	}
	got := executor.ExecuteCalls()
	if len(got) != 2 || !strings.HasPrefix(got[0], badAuth.ID+"|") || !strings.HasPrefix(got[1], goodAuth.ID+"|") {
		t.Fatalf("execute calls = %v, want every key exactly once", got)
	}
}

// TestManagerExecute_OpenAICompat429AllKeysRawJSONBodySanitized verifies that when
// all relay channels return a raw JSON 429 body (as the aistudio executor produces),
// the final error returned to the caller has its message sanitized to a generic text
// and does not expose the internal provider details to the user.
func TestManagerExecute_OpenAICompat429AllKeysRawJSONBodySanitized(t *testing.T) {
	executor := &authScopedOpenAICompatPoolExecutor{id: openAICompatPoolProviderKey, executeErrors: map[string]error{}}
	m, badAuth, goodAuth := newTwoAuthOpenAICompatPoolManager(t, executor)
	// Allow all credentials to be attempted in a single pass (no per-round limit).
	m.SetRetryConfig(0, 0, 0)
	// Simulate raw JSON bodies exactly as returned by the aistudio / relay executor.
	relayBody429 := `{"error":{"code":429,"message":"Concurrency limit exceeded for account, please retry later","status":"RESOURCE_EXHAUSTED"}}`
	badErr := &retryAfterStatusError{status: http.StatusTooManyRequests, message: relayBody429}
	goodErr := &retryAfterStatusError{status: http.StatusTooManyRequests, message: relayBody429}
	executor.executeErrors[badAuth.ID] = badErr
	executor.executeErrors[goodAuth.ID] = goodErr

	_, err := m.Execute(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: "claude-opus-4.66"}, cliproxyexecutor.Options{})
	if err == nil {
		t.Fatal("expected error when all keys are rate-limited")
	}
	// Status must remain 429.
	if statusCodeFromError(err) != http.StatusTooManyRequests {
		t.Fatalf("error status = %d, want 429", statusCodeFromError(err))
	}
	// Raw provider JSON must NOT be present in the returned error message.
	if strings.Contains(err.Error(), "Concurrency limit exceeded") {
		t.Fatalf("error message leaks raw provider body: %v", err)
	}
	if strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
		t.Fatalf("error message leaks raw provider body: %v", err)
	}
	// Both credentials must have been attempted before giving up.
	got := executor.ExecuteCalls()
	if len(got) != 2 {
		t.Fatalf("execute calls = %v, want both credentials attempted", got)
	}
}

// TestManagerExecuteStream_OpenAICompat429AllKeysRawJSONBodySanitized is the streaming
// counterpart of TestManagerExecute_OpenAICompat429AllKeysRawJSONBodySanitized.
func TestManagerExecuteStream_OpenAICompat429AllKeysRawJSONBodySanitized(t *testing.T) {
	executor := &authScopedOpenAICompatPoolExecutor{id: openAICompatPoolProviderKey, streamErrors: map[string]error{}}
	m, badAuth, goodAuth := newTwoAuthOpenAICompatPoolManager(t, executor)
	// Allow all credentials to be attempted in a single pass (no per-round limit).
	m.SetRetryConfig(0, 0, 0)
	relayBody429 := `{"error":{"code":429,"message":"Concurrency limit exceeded for account, please retry later","status":"RESOURCE_EXHAUSTED"}}`
	badErr := &retryAfterStatusError{status: http.StatusTooManyRequests, message: relayBody429}
	goodErr := &retryAfterStatusError{status: http.StatusTooManyRequests, message: relayBody429}
	executor.streamErrors[badAuth.ID] = badErr
	executor.streamErrors[goodAuth.ID] = goodErr

	_, err := m.ExecuteStream(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: "claude-opus-4.66"}, cliproxyexecutor.Options{})
	if err == nil {
		t.Fatal("expected error when all keys are rate-limited")
	}
	if statusCodeFromError(err) != http.StatusTooManyRequests {
		t.Fatalf("error status = %d, want 429", statusCodeFromError(err))
	}
	if strings.Contains(err.Error(), "Concurrency limit exceeded") {
		t.Fatalf("stream error message leaks raw provider body: %v", err)
	}
	if strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
		t.Fatalf("stream error message leaks raw provider body: %v", err)
	}
	got := executor.StreamCalls()
	if len(got) != 2 {
		t.Fatalf("stream calls = %v, want both credentials attempted", got)
	}
}

func TestManagerExecuteStream_OpenAICompatAliasPoolPreservesEarlierUpstreamError(t *testing.T) {
	alias := "claude-opus-4.66"
	upstreamErr := &Error{HTTPStatus: http.StatusBadGateway, Message: "first model upstream failed"}
	internalErr := errors.New("second model failed before upstream")
	executor := &openAICompatPoolExecutor{
		id: openAICompatPoolProviderKey,
		streamFirstErrors: map[string]error{
			"deepseek-v3.1": upstreamErr,
			"glm-5":         internalErr,
		},
		streamAttempts: map[string]bool{"deepseek-v3.1": true},
	}
	m := newOpenAICompatPoolTestManager(t, alias, []internalconfig.OpenAICompatibilityModel{
		{Name: "deepseek-v3.1", Alias: alias},
		{Name: "glm-5", Alias: alias},
	}, executor)

	result, errExecute := m.ExecuteStream(context.Background(), []string{openAICompatPoolProviderKey}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
	if result == nil || errExecute != nil {
		t.Fatalf("ExecuteStream() = (%#v, %v), want an error stream", result, errExecute)
	}
	chunk, ok := <-result.Chunks
	if !ok || !errors.Is(chunk.Err, upstreamErr) {
		t.Fatalf("stream error = %v, want earlier upstream error %v", chunk.Err, upstreamErr)
	}
	if errors.Is(chunk.Err, internalErr) {
		t.Fatalf("stream error = %v, later internal error replaced upstream error", chunk.Err)
	}
	if hasUpstreamExecutionAttempt(chunk.Err) {
		t.Fatalf("stream error leaked internal upstream-attempt marker: %T/%v", chunk.Err, chunk.Err)
	}
}
