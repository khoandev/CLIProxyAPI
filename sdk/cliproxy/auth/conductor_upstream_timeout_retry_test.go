package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestHasUpstreamTimeoutMarker(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "exact marker provider ended the request",
			text: "Provider ended the request",
			want: true,
		},
		{
			name: "exact marker chinese upstream timeout",
			text: "上游响应超时",
			want: true,
		},
		{
			name: "full user-supplied chinese timeout message",
			text: "上游响应超时，请稍后重试。原因：上游服务在规定时间内没有返回有效响应，或流式连接长时间没有新数据。解决方案：请稍后重试；如经常出现，可缩短上下文、降低并发。如当前使用智能路由，请先重试；若仍失败，建议切换固定商家；如当前使用固定商家，请切换固定商家或开启智能路由。",
			want: true,
		},
		{
			name: "embedded in json string",
			text: `{"error":{"message":"error: Provider ended the request mid-generation"}}`,
			want: true,
		},
		{
			name: "case sensitive provider ended lowercase should not match",
			text: "provider ended the request",
			want: false,
		},
		{
			name: "ordinary timeout text does not match",
			text: "upstream request timeout: context deadline exceeded",
			want: false,
		},
		{
			name: "unrelated error does not match",
			text: "502 Bad Gateway: connection refused",
			want: false,
		},
		{
			name: "empty string",
			text: "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasUpstreamTimeoutMarker(tt.text); got != tt.want {
				t.Errorf("hasUpstreamTimeoutMarker(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestSemanticUpstreamTimeoutErrorSanitization(t *testing.T) {
	chineseMsg := "上游响应超时，请稍后重试。原因：上游服务在规定时间内没有返回有效响应"
	err := newUpstreamTimeoutError(chineseMsg)

	if err.Code != upstreamTimeoutErrorCode {
		t.Errorf("err.Code = %q, want %q", err.Code, upstreamTimeoutErrorCode)
	}
	if err.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("err.HTTPStatus = %d, want %d", err.HTTPStatus, http.StatusServiceUnavailable)
	}
	if !err.Retryable {
		t.Error("err.Retryable = false, want true")
	}
	if !isUpstreamTimeoutError(err) {
		t.Error("isUpstreamTimeoutError(err) = false, want true")
	}

	// Must never expose Chinese text in user-facing message
	for _, r := range err.Message {
		if unicode.Is(unicode.Han, r) {
			t.Fatalf("err.Message contains Chinese rune %c (%q)", r, err.Message)
		}
	}

	if strings.Contains(err.Message, "上游响应超时") || strings.Contains(err.Message, "Provider ended the request") {
		t.Fatalf("err.Message exposes raw marker: %q", err.Message)
	}

	if err.Message != "upstream provider timeout; retry the request" {
		t.Errorf("err.Message = %q, want sanitized generic message", err.Message)
	}
}

func TestSemanticRetryDelaySchedule(t *testing.T) {
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 1 * time.Second},
		{attempt: 1, want: 2 * time.Second},
		{attempt: 2, want: 4 * time.Second},
		{attempt: 3, want: 4 * time.Second},
		{attempt: 4, want: 4 * time.Second},
		{attempt: 10, want: 4 * time.Second},
	}

	for _, tt := range tests {
		got := semanticRetryDelay(tt.attempt)
		if got != tt.want {
			t.Errorf("semanticRetryDelay(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestSemanticRetryDelayContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := semanticRetryWaitFunc(ctx, 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("semanticRetryWaitFunc with canceled context returned %v, want context.Canceled", err)
	}
}

type fakeTimeoutRetryExecutor struct {
	id           string
	mu           chan struct{}
	calls        int
	recordedReqs []cliproxyexecutor.Request
	responses    []struct {
		resp cliproxyexecutor.Response
		err  error
	}
	streamResponses []struct {
		chunks []cliproxyexecutor.StreamChunk
		err    error
	}
}

func newFakeTimeoutRetryExecutor(id string) *fakeTimeoutRetryExecutor {
	ch := make(chan struct{}, 1)
	ch <- struct{}{}
	return &fakeTimeoutRetryExecutor{id: id, mu: ch}
}

func (e *fakeTimeoutRetryExecutor) Identifier() string {
	return e.id
}

func (e *fakeTimeoutRetryExecutor) Execute(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	<-e.mu
	defer func() { e.mu <- struct{}{} }()

	e.recordedReqs = append(e.recordedReqs, req)
	idx := e.calls
	e.calls++

	if idx < len(e.responses) {
		return e.responses[idx].resp, e.responses[idx].err
	}
	return cliproxyexecutor.Response{}, errors.New("exhausted responses")
}

func (e *fakeTimeoutRetryExecutor) ExecuteStream(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	<-e.mu
	defer func() { e.mu <- struct{}{} }()

	e.recordedReqs = append(e.recordedReqs, req)
	idx := e.calls
	e.calls++

	if idx < len(e.streamResponses) {
		spec := e.streamResponses[idx]
		if spec.err != nil {
			return nil, spec.err
		}
		ch := make(chan cliproxyexecutor.StreamChunk, len(spec.chunks))
		for _, c := range spec.chunks {
			ch <- c
		}
		close(ch)
		return &cliproxyexecutor.StreamResult{Chunks: ch}, nil
	}
	return nil, errors.New("exhausted stream responses")
}

func (e *fakeTimeoutRetryExecutor) Refresh(ctx context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *fakeTimeoutRetryExecutor) HttpRequest(ctx context.Context, auth *Auth, req *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func (e *fakeTimeoutRetryExecutor) CountTokens(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, errors.New("not implemented")
}

func TestManagerExecuteStream_SemanticTimeoutBuffersAndDiscardsPartialOutput(t *testing.T) {
	origWait := semanticRetryWaitFunc
	semanticRetryWaitFunc = func(ctx context.Context, delay time.Duration) error {
		return nil
	}
	defer func() { semanticRetryWaitFunc = origWait }()

	manager := NewManager(nil, nil, nil)
	manager.SetRetryConfig(2, 0, 0)

	provider := "stream-provider"
	model := "stream-model"
	exec := newFakeTimeoutRetryExecutor(provider)
	exec.streamResponses = []struct {
		chunks []cliproxyexecutor.StreamChunk
		err    error
	}{
		{
			chunks: []cliproxyexecutor.StreamChunk{
				{Payload: []byte("data: partial-leaked-attempt-1\n\n")},
				{Err: errors.New("上游响应超时，请稍后重试")},
			},
		},
		{
			chunks: []cliproxyexecutor.StreamChunk{
				{Payload: []byte("data: clean-attempt-2-chunk-1\n\n")},
				{Payload: []byte("data: clean-attempt-2-chunk-2\n\n")},
			},
		},
	}

	manager.RegisterExecutor(exec)
	registry.GetGlobalRegistry().RegisterClient("auth-stream", provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient("auth-stream") })

	if _, err := manager.Register(context.Background(), &Auth{
		ID:       "auth-stream",
		Provider: provider,
		Status:   StatusActive,
		ModelStates: map[string]*ModelState{
			model: {Status: StatusActive},
		},
	}); err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	req := cliproxyexecutor.Request{Model: model, Payload: []byte(`{"stream":true}`)}
	streamResult, err := manager.ExecuteStream(context.Background(), []string{provider}, req, cliproxyexecutor.Options{Stream: true})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}
	if streamResult == nil || streamResult.Chunks == nil {
		t.Fatal("ExecuteStream() returned nil stream result or chunks")
	}

	var received []string
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
		received = append(received, string(chunk.Payload))
	}

	if len(received) != 2 {
		t.Fatalf("received %d chunks, want 2: %#v", len(received), received)
	}
	if received[0] != "data: clean-attempt-2-chunk-1\n\n" || received[1] != "data: clean-attempt-2-chunk-2\n\n" {
		t.Fatalf("received chunks = %#v, want clean-attempt-2 chunks only", received)
	}

	// Double check that partial leaked chunk was discarded
	for _, c := range received {
		if strings.Contains(c, "partial-leaked-attempt-1") {
			t.Fatalf("partial chunk from failed attempt was leaked: %s", c)
		}
	}
}

func TestManagerExecuteStream_CleanStreamPreservesChunks(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	provider := "clean-stream-provider"
	model := "clean-model"
	exec := newFakeTimeoutRetryExecutor(provider)
	exec.streamResponses = []struct {
		chunks []cliproxyexecutor.StreamChunk
		err    error
	}{
		{
			chunks: []cliproxyexecutor.StreamChunk{
				{Payload: []byte("chunk-1")},
				{Payload: []byte("chunk-2")},
				{Payload: []byte("chunk-3")},
			},
		},
	}

	manager.RegisterExecutor(exec)
	registry.GetGlobalRegistry().RegisterClient("auth-clean", provider, []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient("auth-clean") })

	if _, err := manager.Register(context.Background(), &Auth{
		ID:       "auth-clean",
		Provider: provider,
		Status:   StatusActive,
		ModelStates: map[string]*ModelState{
			model: {Status: StatusActive},
		},
	}); err != nil {
		t.Fatalf("Register() error: %v", err)
	}

	req := cliproxyexecutor.Request{Model: model}
	streamResult, err := manager.ExecuteStream(context.Background(), []string{provider}, req, cliproxyexecutor.Options{Stream: true})
	if err != nil {
		t.Fatalf("ExecuteStream() error = %v", err)
	}

	var collected []string
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("unexpected error in chunk: %v", chunk.Err)
		}
		collected = append(collected, string(chunk.Payload))
	}

	if len(collected) != 3 || collected[0] != "chunk-1" || collected[1] != "chunk-2" || collected[2] != "chunk-3" {
		t.Fatalf("collected chunks = %#v, want [chunk-1, chunk-2, chunk-3]", collected)
	}
}
