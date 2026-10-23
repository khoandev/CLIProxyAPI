package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMaskRuleSetMask(t *testing.T) {
	tests := []struct {
		name string
		set  maskRuleSet
		in   string
		want string
	}{
		{
			name: "brand domain and Kira AI token",
			set:  brandOnlySet,
			in:   `{"text": "Visit kiraai.vn with Kira AI and KIRA AI and kira ai"}`,
			want: `{"text": "Visit llmgate.app with Model AI and MODEL AI and model ai"}`,
		},
		{
			name: "brand domain replaced",
			set:  brandOnlySet,
			in:   "kiraai.vn",
			want: "llmgate.app",
		},
		{
			name: "brand uppercase domain replaced",
			set:  brandOnlySet,
			in:   "KIRAAI.VN",
			want: "LLMGATE.APP",
		},
		{
			name: "opencode2api replaced",
			set:  brandOnlySet,
			in:   `{"error":{"message":"opencode2api: quota exceeded"}}`,
			want: `{"error":{"message":"llmgate.app: quota exceeded"}}`,
		},
		{
			name: "uppercase opencode2api replaced",
			set:  brandOnlySet,
			in:   "OPENCODE2API",
			want: "LLMGATE.APP",
		},
		{
			name: "no branding leaves input untouched",
			set:  brandOnlySet,
			in:   `{"ok": true}`,
			want: `{"ok": true}`,
		},
		{
			name: "antigravity domain and bare token",
			set:  brandAntigravitySet,
			in:   `{"text": "Visit antigravity.dev with antigravity and ANTIGRAVITY and Antigravity"}`,
			want: `{"text": "Visit gemini.dev with gemini and GEMINI and Gemini"}`,
		},
		{
			name: "antigravity as substring replaced",
			set:  brandAntigravitySet,
			in:   "antigravity-antigravity",
			want: "gemini-gemini",
		},
		{
			name: "claude domain and bare token",
			set:  brandClaudeSet,
			in:   `{"text": "Visit kiro.dev with kiro and KIRO and Kiro"}`,
			want: `{"text": "Visit claude.ai with claude and CLAUDE and Claude"}`,
		},
		{
			name: "openai domain and gpt token",
			set:  brandOpenAISet,
			in:   `{"text": "Visit kiro.dev with kiro and KIRO and Kiro"}`,
			want: `{"text": "Visit openai.com with gpt and GPT and Gpt"}`,
		},
		{
			name: "kiro.dev must not become claude.dev",
			set:  brandClaudeSet,
			in:   "see kiro.dev now",
			want: "see claude.ai now",
		},
		{
			name: "mixed domain casing",
			set:  brandClaudeSet,
			in:   "Kiro.dev KIRO.DEV kiro.dev",
			want: "Claude.ai CLAUDE.AI claude.ai",
		},
		{
			name: "mixed domain casing openai",
			set:  brandOpenAISet,
			in:   "Kiro.dev KIRO.DEV kiro.dev",
			want: "Openai.com OPENAI.COM openai.com",
		},
		{
			name: "kiro as substring of other word replaced",
			set:  brandClaudeSet,
			in:   "kiro-kiro",
			want: "claude-claude",
		},
		{
			name: "no kiro leaves input untouched",
			set:  brandClaudeSet,
			in:   `{"ok": true}`,
			want: `{"ok": true}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(tt.set.mask([]byte(tt.in))); got != tt.want {
				t.Errorf("mask(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestMaskRuleSetComposition pins which brand families apply to which request.
// Brand must fire on every request, kiro only for masked models, and antigravity
// only for Gemini models.
func TestMaskRuleSetComposition(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		want   maskRuleSet
		absent []string
	}{
		{
			name:  "neutral model gets brand only",
			model: "gpt-4o",
			want:  brandOnlySet,
		},
		{
			name:  "claude model gets brand and kiro",
			model: "claude-haiku-4-5",
			want:  brandClaudeSet,
		},
		{
			name:  "gpt-5.6 model gets brand and kiro",
			model: "gpt-5.6-sol",
			want:  brandOpenAISet,
		},
		{
			name:  "gemini model gets brand and antigravity",
			model: "gemini-2.5-pro",
			want:  brandAntigravitySet,
		},
		{
			name:   "claude request must not mask antigravity",
			model:  "claude-haiku-4-5",
			absent: []string{"antigravity"},
		},
		{
			name:   "gpt-5.6 request must not mask antigravity",
			model:  "gpt-5.6-sol",
			absent: []string{"antigravity"},
		},
		{
			name:   "gemini request must not mask kiro",
			model:  "gemini-2.5-pro",
			absent: []string{"kiro.dev"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
				strings.NewReader(`{"model": "`+tt.model+`"}`))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req

			if len(tt.want.rules) > 0 {
				if got := maskRuleSetForRequest(c); !sameMaskRules(got, tt.want) {
					t.Fatalf("maskRuleSetForRequest(%q) = %d rules, want %d", tt.model, len(got.rules), len(tt.want.rules))
				}
			}
			for _, pattern := range tt.absent {
				if got := string(maskRuleSetForRequest(c).mask([]byte(pattern))); got != pattern {
					t.Errorf("pattern %q was rewritten to %q, want untouched", pattern, got)
				}
			}
		})
	}
}

func sameMaskRules(a, b maskRuleSet) bool {
	if len(a.rules) != len(b.rules) {
		return false
	}
	for i := range a.rules {
		if a.rules[i] != b.rules[i] {
			return false
		}
	}
	return true
}

func TestProfileForModel(t *testing.T) {
	tests := []struct {
		model      string
		wantMasked bool
		wantDomain string
		wantToken  string
	}{
		{"claude-", true, "claude.ai", "claude"},
		{"claude-haiku-4-5", true, "claude.ai", "claude"},
		{"claude-sonnet-4-6", true, "claude.ai", "claude"},
		{"claude-opus-4-7", true, "claude.ai", "claude"},
		{"claude-3-5-sonnet-20241022", true, "claude.ai", "claude"},
		{"CLAUDE-3-7-SONNET", true, "claude.ai", "claude"},
		{"anthropic/claude-3.5-sonnet", true, "claude.ai", "claude"},
		{"gpt-5.6", true, "openai.com", "gpt"},
		{"gpt-5.6-sol", true, "openai.com", "gpt"},
		{"gpt-5.6-luna", true, "openai.com", "gpt"},
		{"vendor/gpt-5.6-sol", true, "openai.com", "gpt"},
		{" GPT-5.6-SOL ", true, "openai.com", "gpt"},
		{"gpt-5.4", false, "", ""},
		{"gpt-5.60", false, "", ""},
		{"gpt-4o", false, "", ""},
		{"gemini-2.5-flash", false, "", ""},
		{"deepseek-chat", false, "", ""},
		{"", false, "", ""},
	}
	for _, tt := range tests {
		got, ok := profileForModel(tt.model)
		if ok != tt.wantMasked {
			t.Errorf("profileForModel(%q) masked = %v, want %v", tt.model, ok, tt.wantMasked)
			continue
		}
		if !ok {
			continue
		}
		if got.domain != tt.wantDomain || got.token != tt.wantToken {
			t.Errorf("profileForModel(%q) = %+v, want {%q %q}", tt.model, got, tt.wantDomain, tt.wantToken)
		}
	}
}

func TestIsGeminiModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"gemini-2.5-flash", true},
		{"gemini-2.5-pro", true},
		{"GEMINI-PRO", true},
		{"antigravity/gemini-2.5-pro", true},
		{"claude-sonnet-4-6", false},
		{"gpt-4o", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isGeminiModel(tt.model); got != tt.want {
			t.Errorf("isGeminiModel(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

// TestMaskingAllModelsNonStreaming covers the neutral, claude, gpt-5.6 and gemini
// request families end to end, including that brand masking fires on all of them.
func TestMaskingAllModelsNonStreaming(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{"gpt-4o", `{"content": "powered by llmgate.app and Model AI"}`},
		{"claude-haiku-4-5", `{"content": "powered by llmgate.app and claude.ai"}`},
		{"gpt-5.6-sol", `{"content": "powered by llmgate.app and openai.com"}`},
		{"gemini-2.5-pro", `{"content": "powered by llmgate.app and gemini"}`},
	}
	for _, tt := range tests {
		t.Run("model="+tt.model, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(MaskingMiddleware())
			r.POST("/v1/chat/completions", func(c *gin.Context) {
				c.Header("Content-Type", "application/json")
				c.Header("Content-Length", "1000")
				c.Status(http.StatusOK)
				c.Writer.WriteString(`{"content": "powered by kiraai.vn and ` + brandProbeForModel(tt.model) + `"}`)
			})

			reqBody := `{"model": "` + tt.model + `", "messages": [{"role": "user", "content": "hi"}]}`
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Body.String(); got != tt.want {
				t.Errorf("body = %q, want %q", got, tt.want)
			}
			if got := rec.Header().Get("Content-Length"); got != "" {
				t.Errorf("Content-Length = %q, want stripped", got)
			}
		})
	}
}

// brandProbeForModel returns a raw branding token that only the profile family
// owning tt.model rewrites, so the test proves the right family fired.
func brandProbeForModel(model string) string {
	switch {
	case strings.Contains(model, "gemini"):
		return "antigravity"
	case strings.Contains(model, "claude"):
		return "kiro.dev"
	case strings.HasPrefix(model, "gpt-5.6"):
		return "kiro.dev"
	default:
		return "Kira AI"
	}
}

func TestMaskingBrandAppliesToEveryModelFamily(t *testing.T) {
	models := []string{"gemini-2.5-flash", "gpt-4o", "claude-3-5-sonnet", "gpt-5.6-sol", "deepseek-chat", ""}
	for _, model := range models {
		t.Run("model="+model, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(MaskingMiddleware())
			r.POST("/v1/chat/completions", func(c *gin.Context) {
				c.Header("Content-Type", "application/json")
				c.Header("Content-Length", "1000")
				c.Status(http.StatusOK)
				c.Writer.WriteString(`{"content": "powered by kiraai.vn and Kira AI and opencode2api"}`)
			})

			reqBody := `{"model": "` + model + `", "messages": [{"role": "user", "content": "hi"}]}`
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if got, want := rec.Body.String(), `{"content": "powered by llmgate.app and Model AI and llmgate.app"}`; got != want {
				t.Errorf("body = %q, want %q", got, want)
			}
			if got := rec.Header().Get("Content-Length"); got != "" {
				t.Errorf("Content-Length = %q, want stripped", got)
			}
		})
	}
}

func TestMaskingNonMaskedModelLeavesKiroUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MaskingMiddleware())
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.Header("Content-Length", "44")
		c.Status(http.StatusOK)
		c.Writer.WriteString(`{"content": "powered by kiro.dev and kiro"}`)
	})

	reqBody := `{"model": "gpt-4o", "messages": [{"role": "user", "content": "hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	// gpt-4o is not a kiro-masked model, so kiro branding must survive.
	if got, want := rec.Body.String(), `{"content": "powered by kiro.dev and kiro"}`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestMaskingStreaming(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		model string
		chunk string
		want  string
	}{
		{
			name:  "claude stream",
			path:  "/v1/messages",
			model: "claude-sonnet-4-6",
			chunk: `data: {"delta": "kiro.dev is kiro and Kira AI"}`,
			want:  `data: {"delta": "claude.ai is claude and Model AI"}`,
		},
		{
			name:  "gpt-5.6 stream",
			path:  "/v1/responses",
			model: "gpt-5.6-sol",
			chunk: `data: {"delta": "kiro.dev is kiro"}`,
			want:  `data: {"delta": "openai.com is gpt"}`,
		},
		{
			name:  "gemini stream",
			path:  "/v1/messages",
			model: "gemini-2.5-flash",
			chunk: `data: {"delta": "antigravity is real"}`,
			want:  `data: {"delta": "gemini is real"}`,
		},
		{
			name:  "neutral stream",
			path:  "/v1/chat/completions",
			model: "gpt-4o",
			chunk: `data: {"delta": "kiraai.vn is Kira AI"}`,
			want:  `data: {"delta": "llmgate.app is Model AI"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(MaskingMiddleware())
			handler := func(c *gin.Context) {
				c.Header("Content-Type", "text/event-stream")
				c.Writer.WriteHeader(http.StatusOK)
				for _, chunk := range []string{tt.chunk + "\n\n", `data: [DONE]` + "\n\n"} {
					if _, err := c.Writer.WriteString(chunk); err != nil {
						t.Errorf("write chunk: %v", err)
					}
					c.Writer.Flush()
				}
			}
			if tt.path == "/v1/responses" {
				r.POST(tt.path, handler)
			} else {
				r.POST(tt.path, handler)
			}

			reqBody := `{"model": "` + tt.model + `", "stream": true}`
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(reqBody))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			body, err := io.ReadAll(rec.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			want := tt.want + "\n\n" + `data: [DONE]` + "\n\n"
			if got := string(body); got != want {
				t.Errorf("stream body = %q, want %q", got, want)
			}
			if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
				t.Errorf("Content-Type = %q, want text/event-stream", got)
			}
		})
	}
}

func TestMaskingPathOrQueryClaude(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MaskingMiddleware())
	r.GET("/v1/models/claude-3-5-sonnet", func(c *gin.Context) {
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.WriteString("kiro.dev host for kiro")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models/claude-3-5-sonnet", nil))

	if got, want := rec.Body.String(), "claude.ai host for claude"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestMaskingPathGPT56(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MaskingMiddleware())
	r.GET("/v1/models/gpt-5.6-sol", func(c *gin.Context) {
		c.Writer.WriteHeader(http.StatusOK)
		c.Writer.WriteString("kiro.dev host for kiro")
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models/gpt-5.6-sol", nil))

	if got, want := rec.Body.String(), "openai.com host for gpt"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

// TestMaskingChunkBoundaryNotCached pins that masking stays per chunk: a token
// arriving in a later chunk is still rewritten, so no result may be memoized
// across chunks.
func TestMaskingChunkBoundaryNotCached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MaskingMiddleware())
	r.POST("/v1/messages", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Writer.WriteHeader(http.StatusOK)
		for _, chunk := range []string{"clean chunk\n\n", "kiro.dev\n\n", "Kira AI\n\n"} {
			if _, err := c.Writer.WriteString(chunk); err != nil {
				t.Errorf("write chunk: %v", err)
			}
			c.Writer.Flush()
		}
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(`{"model": "claude-sonnet-4-6"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	want := "clean chunk\n\nclaude.ai\n\nModel AI\n\n"
	if got := string(body); got != want {
		t.Errorf("stream body = %q, want %q", got, want)
	}
}
