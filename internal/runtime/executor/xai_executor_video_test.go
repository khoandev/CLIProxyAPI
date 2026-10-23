package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// xaiVideoExec builds a minimal XAIExecutor with a test server as the base URL.
func xaiVideoExec(serverURL string) (*XAIExecutor, *cliproxyauth.Auth) {
	ex := NewXAIExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		Attributes: map[string]string{
			"api_key":  "test-xai-key",
			"base_url": serverURL,
		},
	}
	return ex, auth
}

// xaiVideoOptions creates executor Options with openai-video SourceFormat and the given alt.
func xaiVideoOptions(alt string) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString(xaiVideoHandlerType),
		Alt:          alt,
	}
}

// ----- xaiVideoAction unit tests -----

func TestXAIVideoAction_List(t *testing.T) {
	t.Parallel()
	action, videoID, charID := xaiVideoAction(xaiVideoOptions("videos/list"), nil)
	if action != xaiVideoActionGet {
		t.Errorf("action = %v, want xaiVideoActionGet", action)
	}
	if videoID != "" || charID != "" {
		t.Errorf("unexpected ids: videoID=%q charID=%q", videoID, charID)
	}
}

func TestXAIVideoAction_Delete(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"request_id":"del-1"}`)
	action, videoID, charID := xaiVideoAction(xaiVideoOptions("videos/delete"), payload)
	if action != xaiVideoActionDelete {
		t.Errorf("action = %v, want xaiVideoActionDelete", action)
	}
	if videoID != "del-1" {
		t.Errorf("videoID = %q, want del-1", videoID)
	}
	if charID != "" {
		t.Errorf("charID = %q, want empty", charID)
	}
}

func TestXAIVideoAction_Remix(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"request_id":"rem-2"}`)
	action, videoID, charID := xaiVideoAction(xaiVideoOptions("videos/remix"), payload)
	if action != xaiVideoActionRemix {
		t.Errorf("action = %v, want xaiVideoActionRemix", action)
	}
	if videoID != "rem-2" {
		t.Errorf("videoID = %q, want rem-2", videoID)
	}
	if charID != "" {
		t.Errorf("charID = %q, want empty", charID)
	}
}

func TestXAIVideoAction_GetCharacter(t *testing.T) {
	t.Parallel()
	payload := []byte(`{"character_id":"char-9"}`)
	action, videoID, charID := xaiVideoAction(xaiVideoOptions("videos/characters/get"), payload)
	if action != xaiVideoActionGetCharacter {
		t.Errorf("action = %v, want xaiVideoActionGetCharacter", action)
	}
	if videoID != "" {
		t.Errorf("videoID = %q, want empty", videoID)
	}
	if charID != "char-9" {
		t.Errorf("charID = %q, want char-9", charID)
	}
}

func TestXAIVideoAction_Post(t *testing.T) {
	t.Parallel()
	action, videoID, charID := xaiVideoAction(xaiVideoOptions(""), nil)
	if action != xaiVideoActionPost {
		t.Errorf("action = %v, want xaiVideoActionPost", action)
	}
	if videoID != "" || charID != "" {
		t.Errorf("unexpected ids: videoID=%q charID=%q", videoID, charID)
	}
}

// ----- xaiVideoEndpointPath unit tests -----

func TestXAIVideoEndpointPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		requestPath string
		wantPath    string
	}{
		{"generations", "/v1/videos/generations", xaiVideosGenerationsPath},
		{"edits", "/v1/videos/edits", xaiVideosEditsPath},
		{"extensions", "/v1/videos/extensions", xaiVideosExtensionsPath},
		{"characters create", "/v1/videos/characters", xaiVideosCharactersPath},
		{"unknown", "/v1/something", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FromString(xaiVideoHandlerType),
				Metadata: map[string]any{
					cliproxyexecutor.RequestPathMetadataKey: tc.requestPath,
				},
			}
			got := xaiVideoEndpointPath(opts)
			if got != tc.wantPath {
				t.Errorf("xaiVideoEndpointPath(%q) = %q, want %q", tc.requestPath, got, tc.wantPath)
			}
		})
	}
}

// ----- integration round-trip tests (httptest server) -----

func TestXAIExecutorVideosList(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"object":"list"}`))
	}))
	defer server.Close()

	ex, auth := xaiVideoExec(server.URL)
	_, err := ex.Execute(context.Background(), auth,
		cliproxyexecutor.Request{},
		xaiVideoOptions("videos/list"),
	)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != xaiVideosPath {
		t.Errorf("path = %q, want %q", gotPath, xaiVideosPath)
	}
}

func TestXAIExecutorVideosDelete(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deleted":true}`))
	}))
	defer server.Close()

	ex, auth := xaiVideoExec(server.URL)
	payload := []byte(`{"request_id":"vid-del-1"}`)
	_, err := ex.Execute(context.Background(), auth,
		cliproxyexecutor.Request{Payload: payload},
		xaiVideoOptions("videos/delete"),
	)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != xaiVideosPath+"/vid-del-1" {
		t.Errorf("path = %q, want %q", gotPath, xaiVideosPath+"/vid-del-1")
	}
	// DELETE carries no body.
	if len(gotBody) != 0 {
		t.Errorf("body not empty: %q", string(gotBody))
	}
}

func TestXAIExecutorVideosRemix(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"remix-ok","status":"queued"}`))
	}))
	defer server.Close()

	ex, auth := xaiVideoExec(server.URL)
	payload := []byte(`{"request_id":"vid-rmx","prompt":"reverse"}`)
	_, err := ex.Execute(context.Background(), auth,
		cliproxyexecutor.Request{Payload: payload},
		xaiVideoOptions("videos/remix"),
	)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != xaiVideosPath+"/vid-rmx/remix" {
		t.Errorf("path = %q, want %q", gotPath, xaiVideosPath+"/vid-rmx/remix")
	}
	if string(gotBody) != string(payload) {
		t.Errorf("body = %s, want %s", string(gotBody), string(payload))
	}
}

func TestXAIExecutorVideosCreateCharacter(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"char-new"}`))
	}))
	defer server.Close()

	ex, auth := xaiVideoExec(server.URL)
	payload := []byte(`{"video_id":"vid-src","name":"hero"}`)
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString(xaiVideoHandlerType),
		// characters/create is handled via xaiVideoEndpointPath, not xaiVideoAction
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/videos/characters",
		},
	}
	_, err := ex.Execute(context.Background(), auth,
		cliproxyexecutor.Request{Payload: payload},
		opts,
	)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != xaiVideosCharactersPath {
		t.Errorf("path = %q, want %q", gotPath, xaiVideosCharactersPath)
	}
}

func TestXAIExecutorVideosGetCharacter(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"char-7","name":"hero"}`))
	}))
	defer server.Close()

	ex, auth := xaiVideoExec(server.URL)
	payload := []byte(`{"character_id":"char-7"}`)
	_, err := ex.Execute(context.Background(), auth,
		cliproxyexecutor.Request{Payload: payload},
		xaiVideoOptions("videos/characters/get"),
	)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != xaiVideosCharactersPath+"/char-7" {
		t.Errorf("path = %q, want %q", gotPath, xaiVideosCharactersPath+"/char-7")
	}
}

func TestXAIExecutorVideosCreate(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"vid-gen-1"}`))
	}))
	defer server.Close()

	ex, auth := xaiVideoExec(server.URL)
	payload := []byte(`{"model":"grok-imagine-video","prompt":"a waterfall"}`)
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString(xaiVideoHandlerType),
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/videos/generations",
		},
	}
	_, err := ex.Execute(context.Background(), auth,
		cliproxyexecutor.Request{Model: "grok-imagine-video", Payload: payload},
		opts,
	)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != xaiVideosGenerationsPath {
		t.Errorf("path = %q, want %q", gotPath, xaiVideosGenerationsPath)
	}
	if string(gotBody) != string(payload) {
		t.Errorf("body = %s, want %s", string(gotBody), string(payload))
	}
}

// TestXAIExecutorVideosLegacyRetrieve exercises the legacy retrieve path where
// request_id is embedded in the payload with an empty alt.
func TestXAIExecutorVideosLegacyRetrieve(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","video":{"url":"https://cdn.example.com/v.mp4"}}`))
	}))
	defer server.Close()

	ex, auth := xaiVideoExec(server.URL)
	payload := []byte(`{"request_id":"vid-legacy"}`)
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString(xaiVideoHandlerType),
		Alt:          "",
		Metadata:     map[string]any{},
	}
	_, err := ex.Execute(context.Background(), auth,
		cliproxyexecutor.Request{Payload: payload},
		opts,
	)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET (legacy retrieve)", gotMethod)
	}
	if gotPath != xaiVideosPath+"/vid-legacy" {
		t.Errorf("path = %q, want %q", gotPath, xaiVideosPath+"/vid-legacy")
	}
}

// TestXAIExecutorVideosUpstreamError verifies non-2xx responses are turned into errors.
func TestXAIExecutorVideosUpstreamError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	defer server.Close()

	ex, auth := xaiVideoExec(server.URL)
	_, err := ex.Execute(context.Background(), auth,
		cliproxyexecutor.Request{},
		xaiVideoOptions("videos/list"),
	)
	if err == nil {
		t.Fatal("expected error for 429 upstream, got nil")
	}
}
