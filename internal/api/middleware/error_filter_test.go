package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/errorfilter"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/tidwall/gjson"
)

// errorFilterTestRouter mounts the error filter inside a capture middleware so a
// test can read the error entries the filter records after the chain finishes.
func errorFilterTestRouter(t *testing.T, method, path string, handler gin.HandlerFunc) (*gin.Engine, *[]*interfaces.ErrorMessage) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorded := &[]*interfaces.ErrorMessage{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()
		if value, exists := c.Get("API_RESPONSE_ERROR"); exists {
			if apiErrors, ok := value.([]*interfaces.ErrorMessage); ok {
				*recorded = apiErrors
			}
		}
	}, ErrorFilterMiddleware())
	router.Handle(method, path, handler)
	return router, recorded
}

func serveErrorFilterTest(t *testing.T, router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestErrorFilterMiddlewarePromotesCJKErrorEnvelope(t *testing.T) {
	router, recorded := errorFilterTestRouter(t, http.MethodPost, "/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"error": gin.H{
			"message": "配额不足",
			"type":    "insufficient_quota",
			"code":    "insufficient_quota",
		}})
	})

	recorder := serveErrorFilterTest(t, router, http.MethodPost, "/v1/chat/completions", `{}`)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	body := recorder.Body.Bytes()
	if got := gjson.GetBytes(body, "error.message").String(); got != errorfilter.GenericErrorMessage {
		t.Fatalf("message = %q, want %q", got, errorfilter.GenericErrorMessage)
	}
	if got := gjson.GetBytes(body, "error.type").String(); got != "insufficient_quota" {
		t.Fatalf("type = %q, want preserved", got)
	}
	if got := gjson.GetBytes(body, "error.code").String(); got != "insufficient_quota" {
		t.Fatalf("code = %q, want preserved", got)
	}
	if errorfilter.ContainsCJK(string(body)) {
		t.Fatalf("response still contains CJK: %s", body)
	}
	if len(*recorded) == 0 {
		t.Fatal("expected the promoted error to be recorded for request logging")
	}
	if (*recorded)[0].StatusCode != http.StatusBadGateway {
		t.Fatalf("recorded status = %d, want %d", (*recorded)[0].StatusCode, http.StatusBadGateway)
	}
}

func TestErrorFilterMiddlewarePromotesASCIIErrorEnvelope(t *testing.T) {
	router, recorded := errorFilterTestRouter(t, http.MethodPost, "/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"error": gin.H{"message": "rate limited", "type": "rate_limit_error"}})
	})

	recorder := serveErrorFilterTest(t, router, http.MethodPost, "/v1/chat/completions", `{}`)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "error.message").String(); got != "rate limited" {
		t.Fatalf("message = %q, want verbatim", got)
	}
	if len(*recorded) == 0 {
		t.Fatal("expected the promoted error to be recorded for request logging")
	}
}

func TestErrorFilterMiddlewarePassesSuccessThrough(t *testing.T) {
	router, recorded := errorFilterTestRouter(t, http.MethodPost, "/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"id": "x", "choices": []any{}})
	})

	recorder := serveErrorFilterTest(t, router, http.MethodPost, "/v1/chat/completions", `{}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "id").String(); got != "x" {
		t.Fatalf("body = %s, want the handler payload", recorder.Body.Bytes())
	}
	if len(*recorded) != 0 {
		t.Fatalf("success response recorded %d errors, want 0", len(*recorded))
	}
}

func TestErrorFilterMiddlewarePassesLargeSuccessThrough(t *testing.T) {
	payload := `{"id":"x","data":"` + strings.Repeat("a", 8<<10) + `"}`
	router, recorded := errorFilterTestRouter(t, http.MethodPost, "/v1/chat/completions", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json", []byte(payload))
	})

	recorder := serveErrorFilterTest(t, router, http.MethodPost, "/v1/chat/completions", `{}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder.Body.String() != payload {
		t.Fatalf("body changed: got %d bytes, want %d", recorder.Body.Len(), len(payload))
	}
	if len(*recorded) != 0 {
		t.Fatalf("success response recorded %d errors, want 0", len(*recorded))
	}
}

func TestErrorFilterMiddlewareLeavesManagementResponsesAlone(t *testing.T) {
	router, _ := errorFilterTestRouter(t, http.MethodPost, "/v0/management/auth-files", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "error", "error": "失败"})
	})

	recorder := serveErrorFilterTest(t, router, http.MethodPost, "/v0/management/auth-files", `{}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "error").String(); got != "失败" {
		t.Fatalf("body = %s, want the management payload unchanged", recorder.Body.Bytes())
	}
}

func TestErrorFilterMiddlewareSanitizesStreamError(t *testing.T) {
	chunk := "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"请求失败\",\"code\":\"x\"}}\n\n"
	router, recorded := errorFilterTestRouter(t, http.MethodPost, "/v1/messages", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString(chunk)
		c.Writer.Flush()
	})

	recorder := serveErrorFilterTest(t, router, http.MethodPost, "/v1/messages", `{}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (stream status is already committed)", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if errorfilter.ContainsCJK(body) {
		t.Fatalf("stream still contains CJK: %s", body)
	}
	if !strings.Contains(body, `"message":"`+errorfilter.GenericErrorMessage+`"`) {
		t.Fatalf("stream message not sanitized: %s", body)
	}
	if !strings.Contains(body, `"code":"x"`) {
		t.Fatalf("stream lost the original code: %s", body)
	}
	if len(*recorded) == 0 {
		t.Fatal("expected the stream error to be recorded for request logging")
	}
}

func TestErrorFilterMiddlewareSanitizesErrorStatusBody(t *testing.T) {
	router, recorded := errorFilterTestRouter(t, http.MethodPost, "/v1/chat/completions", func(c *gin.Context) {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "参数错误"}})
	})

	recorder := serveErrorFilterTest(t, router, http.MethodPost, "/v1/chat/completions", `{}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if got := gjson.GetBytes(recorder.Body.Bytes(), "error.message").String(); got != errorfilter.GenericErrorMessage {
		t.Fatalf("message = %q, want %q", got, errorfilter.GenericErrorMessage)
	}
	if len(*recorded) == 0 {
		t.Fatal("expected the sanitized error to be recorded for request logging")
	}
}

func TestErrorFilterMiddlewareLeavesPlainTextBodyAlone(t *testing.T) {
	const payload = "internal error"
	router, _ := errorFilterTestRouter(t, http.MethodPost, "/v1/chat/completions", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/plain", []byte(payload))
	})

	recorder := serveErrorFilterTest(t, router, http.MethodPost, "/v1/chat/completions", `{}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if recorder.Body.String() != payload {
		t.Fatalf("body = %q, want %q", recorder.Body.String(), payload)
	}
}

// TestErrorFilterMiddlewareAllowsWebSocketUpgrade guards the hijack path: the
// websocket relay upgrades through c.Writer, so the filter must hand Hijack to
// the transport instead of answering the request itself.
func TestErrorFilterMiddlewareAllowsWebSocketUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	router := gin.New()
	// Production nesting: masking outside, error filter inside.
	router.Use(MaskingMiddleware(), ErrorFilterMiddleware())
	router.GET("/v1/ws", func(c *gin.Context) {
		conn, errUpgrade := upgrader.Upgrade(c.Writer, c.Request, nil)
		if errUpgrade != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		if _, payload, errRead := conn.ReadMessage(); errRead == nil {
			_ = conn.WriteMessage(websocket.TextMessage, payload)
		}
	})
	server := httptest.NewServer(router)
	defer server.Close()

	conn, response, errDial := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/ws", nil)
	if errDial != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial: %v (handshake status %d)", errDial, status)
	}
	defer func() { _ = conn.Close() }()
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d, want %d", response.StatusCode, http.StatusSwitchingProtocols)
	}
	if errWrite := conn.WriteMessage(websocket.TextMessage, []byte("ping")); errWrite != nil {
		t.Fatalf("write message: %v", errWrite)
	}
	if _, payload, errRead := conn.ReadMessage(); errRead != nil {
		t.Fatalf("read message: %v", errRead)
	} else if string(payload) != "ping" {
		t.Fatalf("echo = %q, want ping", payload)
	}
}
