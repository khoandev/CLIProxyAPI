package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestParseRetryDelay_RateLimitedUntil(t *testing.T) {
	resetAt := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
	retryAfter, err := helps.ParseRetryDelay([]byte(`{"message":"rate limited until ` + resetAt + `"}`))
	if err != nil {
		t.Fatalf("helps.ParseRetryDelay() error = %v", err)
	}
	if retryAfter == nil {
		t.Fatal("helps.ParseRetryDelay() returned nil")
	}
	if got := time.Now().Add(*retryAfter); got.Sub(time.Now().Add(5*time.Minute)) > time.Second || got.Sub(time.Now().Add(5*time.Minute)) < -time.Second {
		t.Fatalf("retryAfter instant = %v, want near %v", got, time.Now().Add(5*time.Minute))
	}
}

func TestOpenAICompatEndpointPathMapsExtraEndpoints(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
		want string
	}{
		{name: "search", path: "/v1/search", want: "/search"},
		{name: "ppt", path: "/v1/ppt/generations", want: "/ppt/generations"},
		{name: "psd", path: "/v1/psd/generations", want: "/psd/generations"},
		{name: "editable", path: "/v1/editable-file-tasks", want: "/editable-file-tasks"},
		{name: "files", path: "/files/abc.png", want: "/files/abc.png"},
		{name: "embeddings", path: "/v1/embeddings", want: "/embeddings"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := openAICompatEndpointPath(cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FromString("openai"),
				Metadata: map[string]any{
					cliproxyexecutor.RequestPathMetadataKey: tc.path,
				},
			})
			if got != tc.want {
				t.Fatalf("openAICompatEndpointPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestOpenAICompatExecutorForwardsMethodAndQuery(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tasks":[]}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test",
	}}
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model: "compat-model",
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai"),
		Method:       http.MethodGet,
		Query:        url.Values{"ids": []string{"a", "b"}},
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/editable-file-tasks",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("method = %q, want %q", gotMethod, http.MethodGet)
	}
	if gotPath != "/v1/editable-file-tasks" {
		t.Fatalf("path = %q, want %q", gotPath, "/v1/editable-file-tasks")
	}
	if gotQuery.Get("ids") != "a" {
		t.Fatalf("query ids = %q, want a", gotQuery.Get("ids"))
	}
	if len(gotQuery["ids"]) != 2 || gotQuery["ids"][1] != "b" {
		t.Fatalf("query ids = %#v, want [a b]", gotQuery["ids"])
	}
}

func TestOpenAICompatExecutorPassthroughPOSTSearch(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test",
	}}
	payload := []byte(`{"model":"compat-model","query":"hello"}`)
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "compat-model",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FromString("openai"),
		OriginalRequest: payload,
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/search",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if string(gotBody) != string(payload) {
		t.Fatalf("body = %s, want %s", string(gotBody), string(payload))
	}
}
func TestOpenAICompatExecutorColonEffortUsesSupportedReasoning(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	const compatName = "compat"
	const model = "deepseek-v4-flash"
	const alias = "deepseek-v4-pro"
	executor := NewOpenAICompatExecutor("openai-compatible-compat", &config.Config{
		OpenAICompatibility: []config.OpenAICompatibility{{
			Name: compatName,
			Models: []config.OpenAICompatibilityModel{{
				Name:  model + ":xhigh",
				Alias: alias,
				Thinking: &registry.ThinkingSupport{
					Levels: []string{"low", "medium", "high", "xhigh"},
				},
			}},
		}},
	})
	auth := &cliproxyauth.Auth{Provider: "openai-compatibility", Attributes: map[string]string{
		"base_url":    server.URL + "/v1",
		"api_key":     "test",
		"compat_name": compatName,
	}}
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   model + ":xhigh",
		Payload: []byte(`{"model":"deepseek-v4-flash:xhigh","messages":[{"role":"user","content":"hi"}]}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(gotBody, "model").String(); got != model {
		t.Fatalf("model = %q, want %q; body=%s", got, model, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "reasoning_effort").String(); got != "xhigh" {
		t.Fatalf("reasoning_effort = %q, want xhigh; body=%s", got, string(gotBody))
	}
}

func TestOpenAICompatExecutorUnsupportedColonEffortRemainsModelID(t *testing.T) {
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	const model = "deepseek-v4-flash"
	const requested = model + ":xhigh"
	executor := NewOpenAICompatExecutor("openai-compatible-compat", &config.Config{
		OpenAICompatibility: []config.OpenAICompatibility{{
			Name: "compat",
			Models: []config.OpenAICompatibilityModel{{
				Name:     model,
				Thinking: &registry.ThinkingSupport{Levels: []string{"low", "medium", "high"}},
			}},
		}},
	})
	auth := &cliproxyauth.Auth{Provider: "openai-compatibility", Attributes: map[string]string{
		"base_url":    server.URL + "/v1",
		"api_key":     "test",
		"compat_name": "compat",
	}}
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   requested,
		Payload: []byte(`{"model":"` + requested + `","messages":[{"role":"user","content":"hi"}]}`),
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if got := gjson.GetBytes(gotBody, "model").String(); got != requested {
		t.Fatalf("model = %q, want %q; body=%s", got, requested, string(gotBody))
	}
	if gjson.GetBytes(gotBody, "reasoning_effort").Exists() {
		t.Fatalf("reasoning_effort unexpectedly set; body=%s", string(gotBody))
	}
}
func TestOpenAICompatExecutor_ExecuteRateLimitedUntil(t *testing.T) {
	resetAt := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited until " + resetAt.Format(time.RFC3339)))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": server.URL + "/v1", "api_key": "test"}}
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{Model: "compat-model", Payload: []byte(`{"model":"compat-model"}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")})
	assertOpenAICompatRetryAfterNear(t, err, resetAt)
}

func TestOpenAICompatExecutor_ExecuteStreamRateLimitedUntil(t *testing.T) {
	resetAt := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited until " + resetAt.Format(time.RFC3339)))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": server.URL + "/v1", "api_key": "test"}}
	_, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{Model: "compat-model", Payload: []byte(`{"model":"compat-model","stream":true}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai"), Stream: true})
	assertOpenAICompatRetryAfterNear(t, err, resetAt)
}

func assertOpenAICompatRetryAfterNear(t *testing.T, err error, resetAt time.Time) {
	t.Helper()
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	retryable, ok := err.(interface{ RetryAfter() *time.Duration })
	if !ok || retryable.RetryAfter() == nil {
		t.Fatalf("error = %#v, want RetryAfter", err)
	}
	got := time.Now().Add(*retryable.RetryAfter())
	if delta := got.Sub(resetAt); delta > time.Second || delta < -time.Second {
		t.Fatalf("retryAfter instant = %v, want near %v", got, resetAt)
	}
}

func TestOpenAICompatExecutor_ExecuteRateLimitedEnvelopePreservesRetryAfter(t *testing.T) {
	resetAt := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_exceeded","message":"rate limited until ` + resetAt.Format(time.RFC3339) + `"}}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": server.URL + "/v1", "api_key": "test"}}
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{Model: "compat-model", Payload: []byte(`{"model":"compat-model"}`)}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("openai")})
	assertOpenAICompatRetryAfterNear(t, err, resetAt)
}
