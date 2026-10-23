package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestUnsupportedParamRetry_CodexExecute(t *testing.T) {
	var count atomic.Int32
	var secondBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		n := count.Add(1)
		if n == 1 {
			// First request should contain reasoning
			if !strings.Contains(string(b), "reasoning") {
				t.Errorf("first request missing reasoning, body=%s", string(b))
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"Unsupported parameter: reasoning_effort"}`))
			return
		}
		secondBody = b
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"output\":[]}}\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID: "test-id",
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": server.URL,
		},
	}
	payload := []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"reasoning":{"effort":"high"}}`)
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FromString("codex"),
		OriginalRequest: payload,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if count.Load() != 2 {
		t.Fatalf("expected 2 requests (retry), got %d", count.Load())
	}
	if gjson.GetBytes(secondBody, "reasoning").Exists() || gjson.GetBytes(secondBody, "reasoning_effort").Exists() {
		t.Fatalf("second request still contains reasoning, body=%s", string(secondBody))
	}
}

func TestUnsupportedParamRetry_CodexExecute_NotRetriedForOther400(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail":"Unsupported parameter: temperature"}`))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": server.URL,
		},
	}
	payload := []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"reasoning":{"effort":"high"}}`)
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FromString("codex"),
		OriginalRequest: payload,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if count.Load() != 1 {
		t.Fatalf("expected 1 request (no retry), got %d", count.Load())
	}
}

func TestUnsupportedParamRetry_OpenAICompatExecute(t *testing.T) {
	var count atomic.Int32
	var secondBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		n := count.Add(1)
		if n == 1 {
			if !strings.Contains(string(b), "reasoning_effort") {
				t.Errorf("first request missing reasoning_effort, body=%s", string(b))
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"Unsupported parameter: reasoning_effort"}`))
			return
		}
		secondBody = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": server.URL + "/v1", "api_key": "test"}}
	payload := []byte(`{"model":"compat-model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high"}`)
	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "compat-model",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FromString("openai"),
		OriginalRequest: payload,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if count.Load() != 2 {
		t.Fatalf("expected 2 requests (retry), got %d", count.Load())
	}
	if gjson.GetBytes(secondBody, "reasoning_effort").Exists() || gjson.GetBytes(secondBody, "reasoning.effort").Exists() {
		t.Fatalf("second request still contains reasoning, body=%s", string(secondBody))
	}
}

func TestUnsupportedParamRetry_OpenAICompatStream(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"Unsupported parameter: reasoning_effort"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[]}\n\n"))
		w.(http.Flusher).Flush()
	}))
	defer server.Close()

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"base_url": server.URL + "/v1", "api_key": "test"}}
	payload := []byte(`{"model":"compat-model","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high","stream":true}`)
	_, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "compat-model",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat:    sdktranslator.FromString("openai"),
		OriginalRequest: payload,
		Stream:          true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if count.Load() != 2 {
		t.Fatalf("expected 2 requests (retry), got %d", count.Load())
	}
}
