package middleware

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
)

// maskRule is one replacement applied to a response body.
type maskRule struct {
	from string
	to   string
}

// maskRuleSet is the ordered replacement table for one request together with the
// distinct lowercase ASCII roots its rules cover.
//
// Order matches the former wrapper nesting: with antigravity registered last it
// was the outermost writer, so it rewrote the body first, then brand, then kiro.
// Within kiro the domain must go before the bare token so it never becomes
// <token>.dev, and within brand the kiraai.vn domain must go before "Kira AI".
// No replacement emits text that another family matches, so the order carries no
// cascading effect and only guards within-family cases.
type maskRuleSet struct {
	rules   []maskRule
	needles []string
	// variants holds every cased form of needles that a rule can match, so the
	// response prefilter can use SIMD substring search instead of a folded
	// byte-at-a-time scan.
	variants []string
}

// brandMaskRules hides upstream vendor branding (kiraai.vn, opencode2api) behind
// llmgate.app and Kira AI behind Model AI. These apply to every response.
var brandMaskRules = maskRuleSet{
	rules: []maskRule{
		{"kiraai.vn", "llmgate.app"},
		{"KIRAAI.VN", "LLMGATE.APP"},
		{"opencode2api", "llmgate.app"},
		{"OPENCODE2API", "LLMGATE.APP"},
		{"Kira AI", "Model AI"},
		{"kira ai", "model ai"},
		{"KIRA AI", "MODEL AI"},
	},
	needles: []string{"kira", "opencode"},
}

// antigravityMaskRules hides antigravity behind gemini.
var antigravityMaskRules = maskRuleSet{
	rules: []maskRule{
		{"antigravity", "gemini"},
		{"Antigravity", "Gemini"},
		{"ANTIGRAVITY", "GEMINI"},
	},
	needles: []string{"antigravity"},
}

// maskProfile describes how kiro branding is rewritten for a given request.
// The cased variants are precomputed once per profile so per-chunk masking only
// performs the byte replacements.
type maskProfile struct {
	domain      string // replacement for kiro.dev
	token       string // replacement for the bare "kiro" token
	domainTitle string // titleFirst(domain), replacement for "Kiro.dev"
	domainUpper string // uppercase domain, replacement for "KIRO.DEV"
	tokenTitle  string // titleFirst(token), replacement for "Kiro"
	tokenUpper  string // uppercase token, replacement for "KIRO"
}

var (
	// claudeMaskProfile hides kiro behind Claude branding.
	claudeMaskProfile = newMaskProfile("claude.ai", "claude")
	// openAIMaskProfile hides kiro behind OpenAI branding.
	openAIMaskProfile = newMaskProfile("openai.com", "gpt")
)

// The four rule sets a request can resolve to are built once at init, so masking
// a response never allocates: brand always applies, plus at most one profile
// family, and the profile families are mutually exclusive.
var (
	brandOnlySet        = withNeedleVariants(brandMaskRules)
	brandClaudeSet      = joinMaskRuleSets(brandMaskRules, kiroMaskRules(claudeMaskProfile))
	brandOpenAISet      = joinMaskRuleSets(brandMaskRules, kiroMaskRules(openAIMaskProfile))
	brandAntigravitySet = joinMaskRuleSets(antigravityMaskRules, brandMaskRules)
)

// withNeedleVariants fills the cased needle variants the prefilter scans for.
func withNeedleVariants(set maskRuleSet) maskRuleSet {
	set.variants = needleVariants(set.needles)
	return set
}

// kiroMaskRules builds the kiro replacement table for a profile with every cased
// replacement already resolved.
func kiroMaskRules(profile maskProfile) maskRuleSet {
	return maskRuleSet{
		rules: []maskRule{
			{"kiro.dev", profile.domain},
			{"Kiro.dev", profile.domainTitle},
			{"KIRO.DEV", profile.domainUpper},
			{"kiro", profile.token},
			{"Kiro", profile.tokenTitle},
			{"KIRO", profile.tokenUpper},
		},
		needles: []string{"kiro"},
	}
}

// joinMaskRuleSets concatenates rule sets in application order and collects their
// distinct root needles.
func joinMaskRuleSets(sets ...maskRuleSet) maskRuleSet {
	joined := maskRuleSet{}
	for _, set := range sets {
		joined.rules = append(joined.rules, set.rules...)
		joined.needles = append(joined.needles, set.needles...)
	}
	return withNeedleVariants(joined)
}

// newMaskProfile builds a profile with every cased replacement precomputed.
func newMaskProfile(domain, token string) maskProfile {
	return maskProfile{
		domain:      domain,
		token:       token,
		domainTitle: titleFirst(domain),
		domainUpper: strings.ToUpper(domain),
		tokenTitle:  titleFirst(token),
		tokenUpper:  strings.ToUpper(token),
	}
}

// MaskingMiddleware wraps responses and rewrites every third-party brand so the
// client only sees this service: vendor domains (kiraai.vn, opencode2api) become
// llmgate.app and Kira AI becomes Model AI on all responses, kiro becomes Claude
// or OpenAI branding for masked models, and antigravity becomes gemini for Gemini
// models. All families share one response writer, so a chunk is scanned once per
// distinct root instead of once per middleware. The replacement applies to both
// streaming and non-streaming responses.
func MaskingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer = &maskingResponseWriter{ResponseWriter: c.Writer, ctx: c}
		c.Next()
	}
}

// maskRequestHints holds the request facts both rule checks need, resolved once.
//
// The two checks used to parse the query string and lower-case the URL path
// independently, which duplicated that work for every request.
type maskRequestHints struct {
	model string // "model" query parameter, "" when absent
	path  string // lower-cased URL path
}

// maskHintsContextKey caches the resolved hints for one request.
const maskHintsContextKey = "cpa.masking.hints"

// maskHints resolves the query model and the lower-cased path once per request.
func maskHints(c *gin.Context) maskRequestHints {
	if c == nil || c.Request == nil {
		return maskRequestHints{}
	}
	if cached, ok := c.Get(maskHintsContextKey); ok {
		if hints, ok := cached.(maskRequestHints); ok {
			return hints
		}
	}
	hints := maskRequestHints{model: c.Query("model")}
	if c.Request.URL != nil {
		hints.path = strings.ToLower(c.Request.URL.Path)
	}
	c.Set(maskHintsContextKey, hints)
	return hints
}

// maskRuleSetForRequest resolves which branding applies to a request. Brand always
// applies; kiro and antigravity are mutually exclusive profile families.
func maskRuleSetForRequest(c *gin.Context) maskRuleSet {
	return maskRuleSetForRequestStatus(c, http.StatusOK)
}

// maskRuleSetForRequestStatus resolves the rule set for a response status.
//
// Resolution can read the request body, and a non-success status means the
// request was answered before any handler consumed it (rejected auth, 404, a
// redirect). Reading the body then is not allowed: an unauthenticated client
// can declare a length and never finish sending, and the read would pin the
// connection instead of letting the rejection through. Brand and path/query
// rules still apply in that case, so the response stays masked.
func maskRuleSetForRequestStatus(c *gin.Context, status int) maskRuleSet {
	allowBody := status < http.StatusMultipleChoices
	hints := maskHints(c)
	if shouldMaskAntigravityRequest(c, hints, allowBody) {
		return brandAntigravitySet
	}
	if profile, ok := maskProfileForRequest(c, hints, allowBody); ok {
		if profile == openAIMaskProfile {
			return brandOpenAISet
		}
		return brandClaudeSet
	}
	return brandOnlySet
}

// requestBodyModelContextKey caches the request body model for one request.
const requestBodyModelContextKey = "cpa.masking.requestBodyModel"

// requestBodyModel returns the "model" field of the request body.
//
// Both the antigravity check and the profile check need it, and the body can be
// several megabytes, so the body is read and the model extracted only once per
// request. The body is restored afterwards so downstream handlers still read
// the full payload.
func requestBodyModel(c *gin.Context) string {
	return requestBodyModelIfAllowed(c, true)
}

// requestBodyModelIfAllowed is requestBodyModel with an explicit decision about
// reading the body. It never caches a skipped lookup, so a later caller that is
// allowed to read still resolves the real model.
func requestBodyModelIfAllowed(c *gin.Context, allowBody bool) string {
	if c == nil {
		return ""
	}
	if cached, ok := c.Get(requestBodyModelContextKey); ok {
		model, _ := cached.(string)
		return model
	}
	if !allowBody {
		return ""
	}
	model := ""
	if c.Request != nil && c.Request.Body != nil {
		bodyBytes, err := util.SharedRequestBody(c)
		if err == nil {
			model = util.GetGJSONBytesNoCopy(bodyBytes, "model").String()
		}
	}
	c.Set(requestBodyModelContextKey, model)
	return model
}

// maskingResponseWriter intercepts Write/WriteString to transform the response
// body before it reaches the client. It also removes Content-Length because the
// transformations change byte length; Go's HTTP server then uses chunked encoding.
type maskingResponseWriter struct {
	gin.ResponseWriter
	ctx                   *gin.Context
	set                   maskRuleSet
	rulesResolved         bool
	contentLengthStripped bool
}

// ruleSet resolves the replacement table once, on the first write, because the
// response status decides whether the request body may be read (see
// maskRuleSetForRequestStatus). Resolving eagerly would read the body of a
// request that is about to be rejected, before auth has run.
func (w *maskingResponseWriter) ruleSet(status int) maskRuleSet {
	if w.rulesResolved {
		return w.set
	}
	w.rulesResolved = true
	w.set = maskRuleSetForRequestStatus(w.ctx, status)
	return w.set
}

func (w *maskingResponseWriter) Write(data []byte) (int, error) {
	// Strip Content-Length before the underlying writer emits headers implicitly.
	w.stripContentLength()
	set := w.ruleSet(w.Status())
	return w.ResponseWriter.Write(set.mask(data))
}

func (w *maskingResponseWriter) WriteString(str string) (int, error) {
	w.stripContentLength()
	set := w.ruleSet(w.Status())
	if !set.containsNeedleString(str) {
		return w.ResponseWriter.WriteString(str)
	}
	return w.ResponseWriter.Write(set.mask([]byte(str)))
}

func (w *maskingResponseWriter) WriteHeader(code int) {
	w.stripContentLength()
	w.ruleSet(code)
	w.ResponseWriter.WriteHeader(code)
}

// stripContentLength removes Content-Length once. It must run on every entry
// point that can emit headers, because a later chunk may still rewrite the body.
func (w *maskingResponseWriter) stripContentLength() {
	if w.contentLengthStripped {
		return
	}
	w.Header().Del("Content-Length")
	w.contentLengthStripped = true
}

// mask applies every rule in order and returns b unchanged, without allocating,
// when none of the covered roots is present.
func (s maskRuleSet) mask(b []byte) []byte {
	if !s.containsNeedleBytes(b) {
		return b
	}
	out := b
	for _, rule := range s.rules {
		out = bytes.ReplaceAll(out, []byte(rule.from), []byte(rule.to))
	}
	return out
}

// maskProfileForRequest checks whether the incoming request targets a masked
// model and returns the branding profile to apply.
func maskProfileForRequest(c *gin.Context, hints maskRequestHints, allowBody bool) (maskProfile, bool) {
	if c == nil || c.Request == nil {
		return maskProfile{}, false
	}

	// 1. Query parameter `model`
	if hints.model != "" {
		if profile, ok := profileForModel(hints.model); ok {
			return profile, true
		}
	}

	// 2. URL path
	if strings.Contains(hints.path, "claude") {
		return claudeMaskProfile, true
	}
	if matchesGPT56Model(hints.path) {
		return openAIMaskProfile, true
	}

	// 3. JSON request body for `model` field
	if profile, ok := profileForModel(requestBodyModelIfAllowed(c, allowBody)); ok {
		return profile, true
	}

	return maskProfile{}, false
}

// profileForModel returns the branding profile for a masked model name:
// Claude models map to Claude, gpt-5.6-* models map to OpenAI.
func profileForModel(model string) (maskProfile, bool) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return maskProfile{}, false
	}
	if strings.Contains(m, "claude") {
		return claudeMaskProfile, true
	}
	if matchesGPT56Model(m) {
		return openAIMaskProfile, true
	}
	return maskProfile{}, false
}

// matchesGPT56Model reports whether m names a gpt-5.6 model, ignoring any
// namespace or vendor segment (e.g. "gpt-5.6-sol", "vendor/gpt-5.6-sol").
func matchesGPT56Model(m string) bool {
	if idx := strings.LastIndex(m, "/"); idx >= 0 {
		m = m[idx+1:]
	}
	const prefix = "gpt-5.6"
	if !strings.HasPrefix(m, prefix) {
		return false
	}
	rest := m[len(prefix):]
	return rest == "" || rest[0] == '-' || rest[0] == '.'
}

// shouldMaskAntigravityRequest checks whether the incoming request is targeting a Gemini model.
func shouldMaskAntigravityRequest(c *gin.Context, hints maskRequestHints, allowBody bool) bool {
	if c == nil || c.Request == nil {
		return false
	}

	// 1. Check query parameter `model`
	if hints.model != "" {
		if isGeminiModel(hints.model) {
			return true
		}
	}

	// 2. Check URL path
	if strings.Contains(hints.path, "gemini") {
		return true
	}

	// 3. Check JSON request body for `model` field
	if isGeminiModel(requestBodyModelIfAllowed(c, allowBody)) {
		return true
	}

	return false
}

// isGeminiModel returns true if the model name indicates a Gemini model.
func isGeminiModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "gemini")
}

// titleFirst upper-cases the first character, matching Kiro -> Claude casing.
func titleFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
