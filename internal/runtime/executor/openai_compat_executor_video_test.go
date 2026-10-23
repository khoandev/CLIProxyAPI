package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

// videoOpts returns an Options with SourceFormat=openai-video and an optional alt tag.
func videoOpts(alt string) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString(openAICompatVideoHandlerType),
		Alt:          alt,
	}
}

// TestOpenAICompatIsVideoRequest checks that only openai-video source format is detected.
func TestOpenAICompatIsVideoRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		format string
		want   bool
	}{
		{openAICompatVideoHandlerType, true},
		{"openai", false},
		{"openai-image", false},
		{"", false},
	}
	for _, tc := range cases {
		opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString(tc.format)}
		if got := openAICompatIsVideoRequest(opts); got != tc.want {
			t.Errorf("openAICompatIsVideoRequest(%q) = %v, want %v", tc.format, got, tc.want)
		}
	}
}

// TestOpenAICompatVideoMethodAndEndpoint checks every alt mapping.
func TestOpenAICompatVideoMethodAndEndpoint(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		alt        string
		payload    []byte
		wantMethod string
		wantEp     string
	}{
		{
			name:       "list",
			alt:        "videos/list",
			wantMethod: http.MethodGet,
			wantEp:     "/videos",
		},
		{
			name:       "delete with id",
			alt:        "videos/delete",
			payload:    []byte(`{"request_id":"vid-abc"}`),
			wantMethod: http.MethodDelete,
			wantEp:     "/videos/vid-abc",
		},
		{
			name:       "delete without id",
			alt:        "videos/delete",
			wantMethod: http.MethodDelete,
			wantEp:     "/videos",
		},
		{
			name:       "remix with id",
			alt:        "videos/remix",
			payload:    []byte(`{"request_id":"vid-xyz"}`),
			wantMethod: http.MethodPost,
			wantEp:     "/videos/vid-xyz/remix",
		},
		{
			name:       "remix without id",
			alt:        "videos/remix",
			wantMethod: http.MethodPost,
			wantEp:     "/videos/generations",
		},
		{
			name:       "characters get with id",
			alt:        "videos/characters/get",
			payload:    []byte(`{"character_id":"char-1"}`),
			wantMethod: http.MethodGet,
			wantEp:     "/videos/characters/char-1",
		},
		{
			name:       "characters get without id",
			alt:        "videos/characters/get",
			wantMethod: http.MethodGet,
			wantEp:     "/videos/characters",
		},
		{
			name:       "characters create",
			alt:        "videos/characters/create",
			wantMethod: http.MethodPost,
			wantEp:     "/videos/characters",
		},
		{
			name:       "default create (empty alt)",
			alt:        "",
			wantMethod: http.MethodPost,
			wantEp:     "/videos/generations",
		},
		{
			name:       "path suffix generations",
			alt:        "",
			wantMethod: http.MethodPost,
			wantEp:     "/videos/generations",
		},
		{
			name:       "path suffix edits",
			alt:        "other",
			wantMethod: http.MethodPost,
			wantEp:     "/videos/generations", // no matching suffix → default
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts := videoOpts(tc.alt)
			method, ep := openAICompatVideoMethodAndEndpoint(opts, tc.payload)
			if method != tc.wantMethod {
				t.Errorf("method = %q, want %q", method, tc.wantMethod)
			}
			if ep != tc.wantEp {
				t.Errorf("endpoint = %q, want %q", ep, tc.wantEp)
			}
		})
	}
}

// TestOpenAICompatVideoMethodAndEndpointPathSuffix verifies that the request-path metadata
// is used to derive the endpoint when no recognized alt is set.
func TestOpenAICompatVideoMethodAndEndpointPathSuffix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		requestPath string
		wantEp      string
	}{
		{"/v1/videos/generations", "/videos/generations"},
		{"/v1/videos/edits", "/videos/edits"},
		{"/v1/videos/extensions", "/videos/extensions"},
		{"/v1/videos", "/videos"},
		{"/something/else", "/videos/generations"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.requestPath, func(t *testing.T) {
			t.Parallel()
			opts := cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FromString(openAICompatVideoHandlerType),
				Alt:          "",
				Metadata: map[string]any{
					cliproxyexecutor.RequestPathMetadataKey: tc.requestPath,
				},
			}
			_, ep := openAICompatVideoMethodAndEndpoint(opts, nil)
			if ep != tc.wantEp {
				t.Errorf("path=%q: endpoint = %q, want %q", tc.requestPath, ep, tc.wantEp)
			}
		})
	}
}

// TestOpenAICompatExecutorVideoList verifies GET /videos is forwarded correctly.
func TestOpenAICompatExecutorVideoList(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"object":"list"}`))
	}))
	defer server.Close()

	ex := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test-key",
	}}
	_, err := ex.Execute(context.Background(), auth, cliproxyexecutor.Request{},
		cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString(openAICompatVideoHandlerType),
			Alt:          "videos/list",
			Query:        url.Values{"limit": []string{"5"}, "order": []string{"desc"}},
		})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/videos" {
		t.Errorf("path = %q, want /v1/videos", gotPath)
	}
	if gotQuery.Get("limit") != "5" {
		t.Errorf("query limit = %q, want 5", gotQuery.Get("limit"))
	}
}

// TestOpenAICompatExecutorVideoDelete verifies DELETE /videos/:id is forwarded correctly.
func TestOpenAICompatExecutorVideoDelete(t *testing.T) {
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

	ex := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test-key",
	}}
	payload := []byte(`{"request_id":"vid-123"}`)
	_, err := ex.Execute(context.Background(), auth, cliproxyexecutor.Request{Payload: payload},
		cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString(openAICompatVideoHandlerType),
			Alt:          "videos/delete",
		})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/v1/videos/vid-123" {
		t.Errorf("path = %q, want /v1/videos/vid-123", gotPath)
	}
	// DELETE should send no body
	if len(gotBody) != 0 {
		t.Errorf("body = %q, want empty", string(gotBody))
	}
}

// TestOpenAICompatExecutorVideoRemix verifies POST /videos/:id/remix is forwarded with body.
func TestOpenAICompatExecutorVideoRemix(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"remix-1","status":"queued"}`))
	}))
	defer server.Close()

	ex := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test-key",
	}}
	payload := []byte(`{"request_id":"vid-456","prompt":"loop it"}`)
	_, err := ex.Execute(context.Background(), auth, cliproxyexecutor.Request{Payload: payload},
		cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString(openAICompatVideoHandlerType),
			Alt:          "videos/remix",
		})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/videos/vid-456/remix" {
		t.Errorf("path = %q, want /v1/videos/vid-456/remix", gotPath)
	}
	if string(gotBody) != string(payload) {
		t.Errorf("body = %s, want %s", string(gotBody), string(payload))
	}
}

// TestOpenAICompatExecutorVideoCreateCharacter verifies POST /videos/characters.
func TestOpenAICompatExecutorVideoCreateCharacter(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"char-new"}`))
	}))
	defer server.Close()

	ex := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test-key",
	}}
	payload := []byte(`{"video_id":"vid-789","name":"hero"}`)
	_, err := ex.Execute(context.Background(), auth, cliproxyexecutor.Request{Payload: payload},
		cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString(openAICompatVideoHandlerType),
			Alt:          "videos/characters/create",
		})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/videos/characters" {
		t.Errorf("path = %q, want /v1/videos/characters", gotPath)
	}
}

// TestOpenAICompatExecutorVideoGetCharacter verifies GET /videos/characters/:id.
func TestOpenAICompatExecutorVideoGetCharacter(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"char-42","name":"hero"}`))
	}))
	defer server.Close()

	ex := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test-key",
	}}
	payload := []byte(`{"character_id":"char-42"}`)
	_, err := ex.Execute(context.Background(), auth, cliproxyexecutor.Request{Payload: payload},
		cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString(openAICompatVideoHandlerType),
			Alt:          "videos/characters/get",
		})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotPath != "/v1/videos/characters/char-42" {
		t.Errorf("path = %q, want /v1/videos/characters/char-42", gotPath)
	}
}

// TestOpenAICompatExecutorVideoCreate verifies the default POST /videos/generations create path.
func TestOpenAICompatExecutorVideoCreate(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"req-gen-1","status":"queued"}`))
	}))
	defer server.Close()

	ex := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test-key",
	}}
	payload := []byte(`{"model":"sora-2","prompt":"a sunset"}`)
	_, err := ex.Execute(context.Background(), auth, cliproxyexecutor.Request{Payload: payload},
		cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString(openAICompatVideoHandlerType),
			Alt:          "",
		})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/videos/generations" {
		t.Errorf("path = %q, want /v1/videos/generations", gotPath)
	}
	if string(gotBody) != string(payload) {
		t.Errorf("body = %s, want %s", string(gotBody), string(payload))
	}
}

// TestOpenAICompatExecutorVideoUpstreamError verifies that a 4xx from the upstream is
// propagated as an error rather than silently swallowed.
func TestOpenAICompatExecutorVideoUpstreamError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"message":"prompt is required"}}`))
	}))
	defer server.Close()

	ex := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test-key",
	}}
	_, err := ex.Execute(context.Background(), auth, cliproxyexecutor.Request{Payload: []byte(`{}`)},
		cliproxyexecutor.Options{
			SourceFormat: sdktranslator.FromString(openAICompatVideoHandlerType),
			Alt:          "",
		})
	if err == nil {
		t.Fatal("expected error for 422 upstream, got nil")
	}
}

// TestOpenAICompatImageEndpointPathVariations verifies that /images/variations
// is correctly mapped to the upstream endpoint path.
func TestOpenAICompatImageEndpointPathVariations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		requestPath string
		wantEp      string
	}{
		{"/v1/images/generations", openAICompatImagesGenerationsPath},
		{"/v1/images/edits", openAICompatImagesEditsPath},
		{"/v1/images/variations", openAICompatImagesVariationsPath},
		// unknown path falls back to default (generations)
		{"/v1/images/unknown", openAICompatDefaultImageEndpoint},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.requestPath, func(t *testing.T) {
			t.Parallel()
			opts := cliproxyexecutor.Options{
				SourceFormat: sdktranslator.FromString(openAICompatImageHandlerType),
				Metadata: map[string]any{
					cliproxyexecutor.RequestPathMetadataKey: tc.requestPath,
				},
			}
			got := openAICompatImageEndpointPath(opts)
			if got != tc.wantEp {
				t.Errorf("path=%q: endpoint = %q, want %q", tc.requestPath, got, tc.wantEp)
			}
		})
	}
}

// TestOpenAICompatExecutorImageVariations verifies that a POST /images/variations
// request is forwarded as POST {base}/images/variations by the executor.
func TestOpenAICompatExecutorImageVariations(t *testing.T) {
	t.Parallel()
	var gotMethod, gotPath string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"abc"}]}`))
	}))
	defer server.Close()

	ex := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test-key",
	}}
	payload := []byte(`{"model":"dall-e-2","n":1}`)
	_, err := ex.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "dall-e-2",
		Payload: payload,
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString(openAICompatImageHandlerType),
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/images/variations",
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/images/variations" {
		t.Errorf("path = %q, want /v1/images/variations", gotPath)
	}
	if string(gotBody) != string(payload) {
		t.Errorf("body = %s, want %s", string(gotBody), string(payload))
	}
}

func TestOpenAICompatExecutor_VideoInput(t *testing.T) {
	for _, source := range []string{"openai", "openai-response"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", source, stream), func(t *testing.T) {
				type capturedRequest struct {
					path string
					body []byte
				}
				requests := make(chan capturedRequest, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, errRead := io.ReadAll(r.Body)
					if errRead != nil {
						http.Error(w, errRead.Error(), http.StatusBadRequest)
						return
					}
					requests <- capturedRequest{path: r.URL.Path, body: body}
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-video\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"video-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"blue, red, green\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl-video\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"video-model\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"chatcmpl-video","object":"chat.completion","created":1,"model":"video-model","choices":[{"index":0,"message":{"role":"assistant","content":"blue, red, green"},"finish_reason":"stop"}]}`)
				}))
				defer server.Close()

				executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
				auth := &cliproxyauth.Auth{
					Provider: "openai-compatibility",
					Attributes: map[string]string{
						"base_url": server.URL + "/v1",
						"api_key":  "test-key",
					},
				}
				payload := `{"model":"client-alias","messages":[{"role":"user","content":[{"type":"text","text":"Describe the videos."},{"type":"video_url","video_url":{"url":"https://example.com/clip.mp4?part=1&name=a%20b","processing":"agentic"}},{"type":"video_url","video_url":{"url":"data:video/mp4;base64,AAECAwQ="}}]}]}`
				if source == "openai-response" {
					payload = `{"model":"client-alias","input":[{"role":"user","content":[{"type":"input_text","text":"Describe the videos."},{"type":"input_video","video_url":"https://example.com/clip.mp4?part=1&name=a%20b","processing":"agentic"},{"type":"input_video","video_url":"data:video/mp4;base64,AAECAwQ="}]}]}`
				}
				req := cliproxyexecutor.Request{Model: "video-model", Payload: []byte(payload)}
				opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString(source), Stream: stream}
				var response strings.Builder
				if stream {
					result, errExecute := executor.ExecuteStream(context.Background(), auth, req, opts)
					if errExecute != nil {
						t.Fatalf("ExecuteStream: %v", errExecute)
					}
					for chunk := range result.Chunks {
						if chunk.Err != nil {
							t.Fatalf("stream chunk: %v", chunk.Err)
						}
						response.Write(chunk.Payload)
					}
				} else {
					result, errExecute := executor.Execute(context.Background(), auth, req, opts)
					if errExecute != nil {
						t.Fatalf("Execute: %v", errExecute)
					}
					response.Write(result.Payload)
				}
				if !strings.Contains(response.String(), "blue, red, green") {
					t.Fatalf("upstream response was lost: %s", response.String())
				}

				var captured capturedRequest
				select {
				case captured = <-requests:
				default:
					t.Fatal("no upstream request")
				}
				if captured.path != "/v1/chat/completions" {
					t.Fatalf("upstream path = %q, want /v1/chat/completions", captured.path)
				}
				if got := gjson.GetBytes(captured.body, "model").String(); got != "video-model" {
					t.Fatalf("upstream model = %q, want video-model", got)
				}
				content := gjson.GetBytes(captured.body, "messages.0.content").Array()
				if len(content) != 3 {
					t.Fatalf("upstream content has %d parts, want text and two videos: %s", len(content), captured.body)
				}
				if content[0].Get("text").String() != "Describe the videos." {
					t.Fatalf("text changed: %s", content[0].Raw)
				}
				for i, wantURL := range []string{"https://example.com/clip.mp4?part=1&name=a%20b", "data:video/mp4;base64,AAECAwQ="} {
					part := content[i+1]
					if part.Get("type").String() != "video_url" || part.Get("video_url.url").String() != wantURL {
						t.Fatalf("video %d changed: %s", i, part.Raw)
					}
				}
				if got := content[1].Get("video_url.processing").String(); got != "agentic" {
					t.Fatalf("video processing = %q, want agentic", got)
				}
			})
		}
	}
}
