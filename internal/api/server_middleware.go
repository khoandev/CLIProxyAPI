package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	codexlive "github.com/router-for-me/CLIProxyAPI/v8/internal/client/codex/live"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/home"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/memlimit"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/safemode"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v8/sdk/access"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

var corsExposedResponseHeaders = []string{
	logging.CPATraceIDHeader,
	"X-CPA-VERSION",
	"X-CPA-COMMIT",
	"X-CPA-BUILD-DATE",
	"X-CPA-SUPPORT-PLUGIN",
	"X-CPA-HOME-VERSION",
	"X-CPA-HOME-BUILD-DATE",
	"X-SERVER-VERSION",
	"X-SERVER-BUILD-DATE",
	"Location",
	"Retry-After",
	// Cross-origin management clients must read the config version token to
	// send it back as If-Match on writes.
	"ETag",
	"X-Request-Id",
	"OpenAI-Request-Id",
}

var corsExposedResponseHeadersJoined = strings.Join(corsExposedResponseHeaders, ", ")

// corsAllowedRequestHeaders lists the request headers browser clients may
// send to the API. The Fetch standard treats Authorization as a CORS
// non-wildcard request-header name, so it must be named explicitly: a
// wildcard does not cover it when a browser enforces that rule.
var corsAllowedRequestHeaders = []string{
	"Authorization",
	"Content-Type",
	"X-Api-Key",
	"X-Goog-Api-Key",
	"Anthropic-Version",
	"Anthropic-Beta",
}

const (
	exampleAPIKeyManagementPath = "/management.html"
	exampleAPIKeyManagementURL  = "/management.html?safe-mode=configure"
)

func (s *Server) maxRequestBodyBytes() int64 {
	if s == nil {
		return 0
	}
	cfg := s.getConfig()
	if cfg == nil || cfg.MaxRequestBodyBytes <= 0 {
		return 0
	}
	return cfg.MaxRequestBodyBytes
}

func (s *Server) maxRequestBodyBytesMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		maxBytes := s.maxRequestBodyBytes()
		if maxBytes <= 0 || c == nil || c.Request == nil {
			c.Next()
			return
		}
		c.Set(handlers.MaxRequestBodyBytesContextKey, maxBytes)
		if c.Request.ContentLength > maxBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": "http: request body too large",
			})
			return
		}
		if c.Request.Body != nil && c.Request.Body != http.NoBody {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

func (s *Server) homeHeartbeatMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := s.getConfig()
		if s == nil || cfg == nil || !cfg.Home.Enabled {
			c.Next()
			return
		}
		if c != nil && c.Request != nil {
			if isManagementAssetPath(c.Request.URL.Path) {
				c.Next()
				return
			}
		}
		client := home.Current()
		if client == nil || !client.HeartbeatOK() {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		c.Next()
	}
}

// isManagementAssetPath reports whether path belongs to the management UI or
// management API surface. Those paths stay reachable when the proxy is
// saturated or the Home heartbeat is down so operators can always reach the
// control plane.
func isManagementAssetPath(path string) bool {
	switch {
	case path == "/v0/management", path == "/v8/management",
		path == "/v0/resource/plugins", path == exampleAPIKeyManagementPath:
		return true
	case strings.HasPrefix(path, "/v0/management/"), strings.HasPrefix(path, "/v8/management/"),
		strings.HasPrefix(path, "/v0/resource/plugins/"):
		return true
	}
	return false
}

// inFlightRequestsPerRequestBytes estimates the steady-state memory held by
// one in-flight proxied request: body copy, translator buffers, stream
// scanner, and accumulator state.
const inFlightRequestsPerRequestBytes = 8 << 20

// maxInFlightRequests resolves the effective in-flight cap from config and the
// detected soft memory limit. It returns 0 when unlimited.
func (s *Server) maxInFlightRequests() int64 {
	if s == nil {
		return 0
	}
	cfg := s.getConfig()
	var configured int
	if cfg != nil {
		configured = cfg.MaxInFlightRequests
	}
	if configured < 0 {
		return 0
	}
	if configured > 0 {
		return int64(configured)
	}
	memLimit := memlimit.Limit()
	if memLimit <= 0 {
		return 0
	}
	derived := memLimit / inFlightRequestsPerRequestBytes
	if derived < 8 {
		derived = 8
	}
	return derived
}

// inFlightAdmissionMiddleware caps concurrent in-flight proxied requests so
// memory stays bounded under load instead of tripping the kernel OOM killer.
// Requests over the cap receive 429 with a Retry-After header; management
// paths bypass the cap. A stream holds its slot for the whole stream duration
// because it holds memory until the handler returns.
func (s *Server) inFlightAdmissionMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s == nil || c == nil || c.Request == nil || isManagementAssetPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		limit := s.maxInFlightRequests()
		current := s.inFlightRequests.Add(1)
		if limit > 0 && current > limit {
			s.inFlightRequests.Add(-1)
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many in-flight requests"})
			return
		}
		defer s.inFlightRequests.Add(-1)
		c.Next()
	}
}

func (s *Server) exampleAPIKeySafeModeRequired(cfg *config.Config) bool {
	return s != nil && s.exampleAPIKeySafeModeEnabled && cfg != nil && safemode.HasExampleAPIKeys(cfg.APIKeys)
}

func (s *Server) exampleAPIKeySafeModeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s == nil || !s.exampleAPIKeySafeModeActive.Load() || c == nil || c.Request == nil || c.Request.URL == nil {
			c.Next()
			return
		}

		path := c.Request.URL.Path
		if path == exampleAPIKeyManagementPath && c.Query("safe-mode") == "configure" {
			c.Next()
			return
		}
		if (path == "/" || path == exampleAPIKeyManagementPath) && (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) {
			s.serveExampleAPIKeyWarningPage(c)
			return
		}
		if !isExampleAPIKeySafeModeProxyPath(path) {
			c.Next()
			return
		}

		c.Header("X-CPA-SAFE-MODE", "example-api-key")
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":   "unsafe_example_api_key",
			"message": "Proxy API endpoints are disabled because api-keys contains template values. Open /management.html?safe-mode=configure, update api-keys in Management, then retry.",
		})
	}
}

func (s *Server) serveExampleAPIKeyWarningPage(c *gin.Context) {
	cfg := s.getConfig()
	var keys []string
	if cfg != nil {
		keys = safemode.ExampleAPIKeys(cfg.APIKeys)
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	if c.Request.Method == http.MethodHead {
		c.Status(http.StatusOK)
		c.Abort()
		return
	}
	c.String(http.StatusOK, safemode.ExampleAPIKeyWarningPageHTML(keys, exampleAPIKeyManagementURL))
	c.Abort()
}

func isExampleAPIKeySafeModeProxyPath(path string) bool {
	switch {
	case path == "/v1" || strings.HasPrefix(path, "/v1/"):
		return true
	case path == "/v1beta" || strings.HasPrefix(path, "/v1beta/"):
		return true
	case path == "/openai/v1" || strings.HasPrefix(path, "/openai/v1/"):
		return true
	case path == "/backend-api/codex" || strings.HasPrefix(path, "/backend-api/codex/"):
		return true
	default:
		return false
	}
}

// corsMiddleware returns a Gin middleware handler that adds CORS headers
// to every response, allowing cross-origin requests.
//
// Returns:
//   - gin.HandlerFunc: The CORS middleware handler
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", corsAllowHeadersValue(c.Request))
		c.Header("Access-Control-Expose-Headers", corsExposedResponseHeadersJoined)

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// corsAllowHeadersValue builds the Access-Control-Allow-Headers value for a
// request. It always names the known API request headers explicitly and, for
// preflight requests, also echoes the headers the client asked about, so no
// allowed header depends on the wildcard that strict non-wildcard rules
// reject for Authorization. Echoing requested headers preserves the previous
// wildcard behavior for arbitrary custom client headers without widening
// the allowed origins or enabling credentials.
func corsAllowHeadersValue(req *http.Request) string {
	allowed := make([]string, 0, len(corsAllowedRequestHeaders))
	seen := make(map[string]struct{}, len(corsAllowedRequestHeaders))
	appendHeader := func(name string) {
		name = http.CanonicalHeaderKey(strings.TrimSpace(name))
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		allowed = append(allowed, name)
	}
	for _, name := range corsAllowedRequestHeaders {
		appendHeader(name)
	}
	if req != nil && req.Method == http.MethodOptions {
		for _, name := range strings.Split(req.Header.Get("Access-Control-Request-Headers"), ",") {
			appendHeader(name)
		}
	}
	return strings.Join(allowed, ", ")
}

// AuthMiddleware returns a Gin middleware handler that authenticates requests
// using the configured authentication providers. When no providers are available,
// it allows all requests (legacy behaviour).
func AuthMiddleware(manager *sdkaccess.Manager) gin.HandlerFunc {
	return accessAuthMiddleware(manager, false)
}

func realtimeStandardAuthMiddleware(manager *sdkaccess.Manager) gin.HandlerFunc {
	return accessAuthMiddleware(manager, true)
}

func accessAuthMiddleware(manager *sdkaccess.Manager, realtimeError bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if manager == nil {
			c.Next()
			return
		}

		result, err := manager.Authenticate(c.Request.Context(), c.Request)
		if err == nil {
			if result != nil {
				c.Set("userApiKey", result.Principal)
				c.Set("accessProvider", result.Provider)
				if len(result.Metadata) > 0 {
					c.Set("accessMetadata", result.Metadata)
				}
				reqCtx := coreusage.WithAPIKey(c.Request.Context(), result.Principal)
				reqCtx = coreusage.WithAccessProvider(reqCtx, result.Provider)
				reqCtx = coreusage.WithIsNativeKey(reqCtx, coreusage.IsNativeAccessProvider(result.Provider))
				c.Request = c.Request.WithContext(reqCtx)
			}
			c.Next()
			return
		}

		statusCode := err.HTTPStatusCode()
		if statusCode >= http.StatusInternalServerError {
			log.Errorf("authentication middleware error: %v", err)
		}
		if realtimeError {
			errorType := "authentication_error"
			code := "invalid_api_key"
			if statusCode == http.StatusRequestEntityTooLarge {
				errorType = "invalid_request_error"
				code = "request_body_too_large"
			} else if statusCode >= http.StatusInternalServerError {
				errorType = "server_error"
				code = "authentication_service_error"
			}
			c.AbortWithStatusJSON(statusCode, gin.H{"error": gin.H{
				"message": err.Message,
				"type":    errorType,
				"param":   nil,
				"code":    code,
			}})
			return
		}
		c.AbortWithStatusJSON(statusCode, gin.H{"error": err.Message})
	}
}

func realtimeAuthMiddleware(manager *sdkaccess.Manager, handler *codexlive.Handler) gin.HandlerFunc {
	fallback := realtimeStandardAuthMiddleware(manager)
	return func(c *gin.Context) {
		authorization, matched, errAuthenticate := handler.AuthenticateClientSecret(c.Request)
		if !matched {
			fallback(c)
			return
		}
		if errAuthenticate != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{
				"message": errAuthenticate.Error(),
				"type":    "invalid_request_error",
				"param":   nil,
				"code":    "invalid_realtime_client_secret",
			}})
			return
		}
		principal := authorization.IssuerPrincipal
		if principal == "" {
			principal = authorization.Principal
		}
		provider := authorization.IssuerProvider
		if provider == "" {
			provider = "realtime-client-secret"
		}
		c.Set("userApiKey", principal)
		c.Set("accessProvider", provider)
		reqCtx := coreusage.WithAPIKey(c.Request.Context(), principal)
		reqCtx = coreusage.WithAccessProvider(reqCtx, provider)
		reqCtx = coreusage.WithIsNativeKey(reqCtx, coreusage.IsNativeAccessProvider(provider))
		c.Request = c.Request.WithContext(reqCtx)
		c.Set(codexlive.ClientSecretSessionContextKey, authorization.Session)
		c.Set(codexlive.ClientSecretPrincipalContextKey, authorization.Principal)
		c.Next()
	}
}
