package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// bypassTestCodexErr mirrors the production Codex statusErr shape: a status
// carrier unwrapping to the normalized context-limit cause.
type bypassTestCodexErr struct {
	code  int
	msg   string
	cause *ContextWindowExceededError
}

func (e *bypassTestCodexErr) Error() string { return e.msg }
func (e *bypassTestCodexErr) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.code
}
func (e *bypassTestCodexErr) Unwrap() error {
	if e == nil || e.cause == nil {
		return nil
	}
	return e.cause
}

type codexBypassTestExecutor struct {
	mu    sync.Mutex
	calls []string
	errs  map[string]error
}

func (e *codexBypassTestExecutor) Identifier() string { return "codex" }

func (e *codexBypassTestExecutor) Execute(_ context.Context, auth *Auth, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.mu.Lock()
	e.calls = append(e.calls, auth.ID)
	err := e.errs[auth.ID]
	e.mu.Unlock()
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	return cliproxyexecutor.Response{Payload: []byte("ok:" + req.Model)}, nil
}

func (e *codexBypassTestExecutor) ExecuteStream(_ context.Context, auth *Auth, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	e.mu.Lock()
	e.calls = append(e.calls, auth.ID)
	err := e.errs[auth.ID]
	e.mu.Unlock()
	ch := make(chan cliproxyexecutor.StreamChunk, 1)
	if err != nil {
		ch <- cliproxyexecutor.StreamChunk{Err: err}
	} else {
		ch <- cliproxyexecutor.StreamChunk{Payload: []byte("ok:" + req.Model)}
	}
	close(ch)
	return &cliproxyexecutor.StreamResult{Chunks: ch}, nil
}

func (e *codexBypassTestExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *codexBypassTestExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, errors.New("not implemented")
}

func (e *codexBypassTestExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, errors.New("not implemented")
}

func (e *codexBypassTestExecutor) callAuthIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

func setupCodexBypassManager(t *testing.T, clientA, clientB string, errs map[string]error) (*Manager, *codexBypassTestExecutor) {
	t.Helper()
	registerCodexFallbackModels(t, clientA)
	registerCodexFallbackModels(t, clientB)
	exec := &codexBypassTestExecutor{errs: errs}
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(3, time.Second, 0)
	m.RegisterExecutor(exec)
	for _, id := range []string{clientA, clientB} {
		if _, err := m.Register(context.Background(), &Auth{
			ID:         id,
			Provider:   "codex",
			Attributes: map[string]string{"auth_kind": "oauth"},
		}); err != nil {
			t.Fatalf("register auth: %v", err)
		}
	}
	return m, exec
}

func codexBypassContextErr() error {
	return &bypassTestCodexErr{
		code: http.StatusBadRequest,
		msg:  `{"error":{"type":"invalid_request_error","code":"context_too_large","message":"This model's maximum context length is 272000 tokens, however your messages resulted in 272444 tokens."}}`,
		cause: &ContextWindowExceededError{
			MaxContext:    272000,
			RequestTokens: 272444,
			Provider:      "codex",
			Model:         "gpt-5.4",
			Raw:           "This model's maximum context length is 272000 tokens, however your messages resulted in 272444 tokens.",
		},
	}
}

// ponytail: gpt-5.4 is the terminal model-fallback stop, so the legacy
// same-provider model fallback issues no extra executor calls here and the
// assertion isolates OAuth credential rotation.
func TestCodexContextBypassSkipsSecondOAuthCred(t *testing.T) {
	m, exec := setupCodexBypassManager(t, "codex-bypass-a", "codex-bypass-b", map[string]error{
		"codex-bypass-a": codexBypassContextErr(),
		"codex-bypass-b": codexBypassContextErr(),
	})
	_, err := m.Execute(context.Background(), []string{"codex"},
		cliproxyexecutor.Request{Model: "gpt-5.4"}, cliproxyexecutor.Options{Metadata: map[string]any{}})
	if err == nil {
		t.Fatal("Execute() expected context error, got nil")
	}
	if !errors.Is(err, ErrContextWindowExceeded) {
		t.Fatalf("Execute() error = %v, want errors.Is ErrContextWindowExceeded", err)
	}
	if got := exec.callAuthIDs(); len(got) != 1 {
		t.Fatalf("executor calls = %v, want exactly 1 (second OAuth cred must not be attempted)", got)
	}
}

func TestCodex429StillRotates(t *testing.T) {
	m, exec := setupCodexBypassManager(t, "codex-rotate-a", "codex-rotate-b", map[string]error{
		"codex-rotate-a": &Error{HTTPStatus: http.StatusTooManyRequests, Message: "rate limit exceeded"},
	})
	resp, err := m.Execute(context.Background(), []string{"codex"},
		cliproxyexecutor.Request{Model: "gpt-5.4"}, cliproxyexecutor.Options{Metadata: map[string]any{}})
	if err != nil {
		t.Fatalf("Execute() error = %v, want success via second cred", err)
	}
	if string(resp.Payload) != "ok:gpt-5.4" {
		t.Fatalf("Execute() payload = %q, want ok:gpt-5.4", resp.Payload)
	}
	got := exec.callAuthIDs()
	if len(got) != 2 || got[0] == got[1] {
		t.Fatalf("executor calls = %v, want one call per cred (rotation)", got)
	}
	if errors.Is(err, ErrContextWindowExceeded) {
		t.Fatalf("Execute() error chain unexpectedly matches ErrContextWindowExceeded")
	}
}

func TestCodexUnrelated400NotContext(t *testing.T) {
	m, _ := setupCodexBypassManager(t, "codex-plain-a", "codex-plain-b", map[string]error{
		"codex-plain-a": &Error{HTTPStatus: http.StatusBadRequest, Message: "model not supported"},
		"codex-plain-b": &Error{HTTPStatus: http.StatusBadRequest, Message: "model not supported"},
	})
	_, err := m.Execute(context.Background(), []string{"codex"},
		cliproxyexecutor.Request{Model: "gpt-5.4"}, cliproxyexecutor.Options{Metadata: map[string]any{}})
	if err == nil {
		t.Fatal("Execute() expected error, got nil")
	}
	if errors.Is(err, ErrContextWindowExceeded) {
		t.Fatalf("Execute() error = %v, unrelated 400 must not match ErrContextWindowExceeded", err)
	}
	if isContextWindowExceededError(err) {
		t.Fatalf("isContextWindowExceededError(%v) = true, want false for unrelated 400", err)
	}
}

func TestIsRequestInvalidError_SentinelWithoutStatusOrBody(t *testing.T) {
	if !isRequestInvalidError(fmt.Errorf("wrapped: %w", ErrContextWindowExceeded)) {
		t.Fatal("isRequestInvalidError(sentinel-wrapped) = false, want true")
	}
	unrelated := &Error{HTTPStatus: http.StatusBadRequest, Message: "model not supported"}
	if isContextWindowExceededError(unrelated) {
		t.Fatalf("isContextWindowExceededError(%v) = true, want false for unrelated 400", unrelated)
	}
}
