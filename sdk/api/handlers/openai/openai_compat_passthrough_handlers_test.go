package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"github.com/tidwall/gjson"
)

type openAICompatPassthroughCaptureExecutor struct {
	gotPath string
	gotBody []byte
}

func (e *openAICompatPassthroughCaptureExecutor) Identifier() string { return "openai-compatibility" }

func (e *openAICompatPassthroughCaptureExecutor) Execute(_ context.Context, _ *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (coreexecutor.Response, error) {
	e.gotPath = ""
	if opts.Metadata != nil {
		if raw, ok := opts.Metadata[coreexecutor.RequestPathMetadataKey]; ok {
			if path, ok := raw.(string); ok {
				e.gotPath = path
			}
		}
	}
	e.gotBody = append([]byte(nil), req.Payload...)
	return coreexecutor.Response{Payload: []byte(`{"ok":true}`)}, nil
}

func (e *openAICompatPassthroughCaptureExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	return nil, http.ErrNotSupported
}

func (e *openAICompatPassthroughCaptureExecutor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *openAICompatPassthroughCaptureExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, http.ErrNotSupported
}

func (e *openAICompatPassthroughCaptureExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, http.ErrNotSupported
}

func TestSearchRequiresModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/search", h.Search)

	req := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(`{"query":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if got := gjson.GetBytes(resp.Body.Bytes(), "error.message").String(); !strings.Contains(got, "model is required") {
		t.Fatalf("error message = %q", got)
	}
}

func TestPPTGenerationsProxiesToOpenAICompatProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	executor := &openAICompatPassthroughCaptureExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth := &coreauth.Auth{ID: "compat-auth", Provider: executor.Identifier(), Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register auth: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "compat-alias"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.POST("/v1/ppt/generations", h.PPTGenerations)

	body := `{"model":"compat-alias","prompt":"slides"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/ppt/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.gotPath != "/v1/ppt/generations" {
		t.Fatalf("request path = %q, want %q", executor.gotPath, "/v1/ppt/generations")
	}
	if string(executor.gotBody) != body {
		t.Fatalf("body = %s, want %s", string(executor.gotBody), body)
	}
}

func TestFilesDownloadUsesDynamicPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	executor := &openAICompatPassthroughCaptureExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth := &coreauth.Auth{ID: "compat-auth-files", Provider: executor.Identifier(), Status: coreauth.StatusActive}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("Register auth: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "compat-alias"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})

	base := handlers.NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	h := NewOpenAIAPIHandler(base)
	router := gin.New()
	router.GET("/files/*filepath", h.FilesDownload)

	req := httptest.NewRequest(http.MethodGet, "/files/foo/bar.png?model=compat-alias", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if executor.gotPath != "/files/foo/bar.png" {
		t.Fatalf("request path = %q, want %q", executor.gotPath, "/files/foo/bar.png")
	}
}
