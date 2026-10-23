package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
)

// contextFallbackAttemptedMetadataKey tracks already-tried fallback routes for
// one logical request. It lives in Options.Metadata so the attempted-routes
// set travels with the request without touching global state.
// ponytail: no observed-max cache; candidates re-resolve per request. Add a
// route->model TTL cache here only if selection shows up in profiles.
const contextFallbackAttemptedMetadataKey = "codex_context_fallback_attempted"

// contextFallbackLogMessage is the single concise event for a Codex context
// diversion (success) or its terminal outcome (no eligible ai-provider).
const contextFallbackLogMessage = "codex context exceeded; skipping remaining oauth codex attempts and trying ai-provider fallback"

// contextFallbackRouteKey normalizes a tried route for the attempted set.
func contextFallbackRouteKey(provider, model string) string {
	base := thinking.ParseSuffix(strings.TrimSpace(model)).ModelName
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.ToLower(strings.TrimSpace(base))
}

// contextFallbackExcludedRoutes copies the per-request attempted-routes set,
// always barring the Codex OAuth route after a Codex context failure.
func contextFallbackExcludedRoutes(opts cliproxyexecutor.Options) map[string]bool {
	exclude := map[string]bool{"codex": true}
	if len(opts.Metadata) == 0 {
		return exclude
	}
	raw, ok := opts.Metadata[contextFallbackAttemptedMetadataKey]
	if !ok {
		return exclude
	}
	switch attempted := raw.(type) {
	case map[string]struct{}:
		for route := range attempted {
			exclude[route] = true
		}
	case []string:
		for _, route := range attempted {
			exclude[route] = true
		}
	}
	return exclude
}

// withContextFallbackAttempted returns opts with route added to the
// attempted-routes set, cloning the metadata map so callers are unaffected.
func withContextFallbackAttempted(opts cliproxyexecutor.Options, provider, model string) cliproxyexecutor.Options {
	attempted := map[string]struct{}{contextFallbackRouteKey(provider, model): {}}
	if raw, ok := opts.Metadata[contextFallbackAttemptedMetadataKey]; ok {
		if prev, ok := raw.(map[string]struct{}); ok {
			for route := range prev {
				attempted[route] = struct{}{}
			}
		}
	}
	metadata := make(map[string]any, len(opts.Metadata)+1)
	for key, value := range opts.Metadata {
		metadata[key] = value
	}
	metadata[contextFallbackAttemptedMetadataKey] = attempted
	opts.Metadata = metadata
	return opts
}

// contextFallbackRouteExcluded reports whether provider/model is ineligible via
// the exclude set (bare provider, model, or provider+model route keys).
func contextFallbackRouteExcluded(exclude map[string]bool, provider, model string) bool {
	if len(exclude) == 0 {
		return false
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.ToLower(strings.TrimSpace(model))
	base := strings.ToLower(strings.TrimSpace(thinking.ParseSuffix(model).ModelName))
	return exclude[provider] || exclude[model] || exclude[base] ||
		exclude[provider+"\x00"+model] || exclude[provider+"\x00"+base]
}

// isCodexContextWindowExceeded reports a terminal Codex context-limit failure.
// The normalized sentinel is authoritative; the legacy substring classifier
// covers errors raised before sibling call sites wrap the sentinel.
func isCodexContextWindowExceeded(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrContextWindowExceeded) {
		return true
	}
	return isContextWindowExceededError(err)
}

// contextFallbackRequestTokens returns the failing input size: parsed
// RequestTokens when the typed error carries it, else a len/4 estimate.
// ponytail: estimate ceiling is tokenizer-shaped; upgrade to a real
// CountTokens call only if fallback misroutes on estimate alone.
func contextFallbackRequestTokens(cause error, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (tokens, maxContext int) {
	var contextErr *ContextWindowExceededError
	if errors.As(cause, &contextErr) && contextErr != nil {
		tokens, maxContext = contextErr.RequestTokens, contextErr.MaxContext
		if tokens > 0 {
			return tokens, maxContext
		}
	}
	payload := opts.OriginalRequest
	if len(payload) == 0 {
		payload = req.Payload
	}
	return len(payload) / 4, maxContext
}

// SelectContextFallback picks an ai-provider route able to serve logicalModel
// at requestTokens input size. Preference: exact same model, then configured
// OAuth alias target. Candidates must advertise enough context, have a
// registered executor, and hold an enabled healthy credential; exclude holds
// attempted routes and barred providers (notably "codex").
func (m *Manager) SelectContextFallback(logicalModel string, requestTokens int, exclude map[string]bool) (provider, model string, ok bool) {
	logicalModel = strings.TrimSpace(logicalModel)
	if m == nil || logicalModel == "" {
		return "", "", false
	}
	for _, candidate := range m.contextFallbackCandidates(logicalModel) {
		if contextFallbackRouteExcluded(exclude, candidate.provider, candidate.model) {
			continue
		}
		if !m.contextFallbackCapacityOK(candidate.model, candidate.provider, requestTokens) {
			continue
		}
		if !m.contextFallbackAuthReady(candidate.provider, candidate.model) {
			continue
		}
		return candidate.provider, candidate.model, true
	}
	return "", "", false
}

type contextFallbackCandidate struct {
	provider string
	model    string
}

// contextFallbackCandidates lists routes in preference order: exact same model
// across providers, then configured alias targets. Codex is never listed, so a
// Codex->provider->Codex loop cannot form.
func (m *Manager) contextFallbackCandidates(logicalModel string) []contextFallbackCandidate {
	var candidates []contextFallbackCandidate
	seen := map[string]struct{}{}
	add := func(provider, model string) {
		provider = strings.TrimSpace(provider)
		model = strings.TrimSpace(model)
		if provider == "" || model == "" || strings.EqualFold(provider, "codex") {
			return
		}
		key := strings.ToLower(provider) + "\x00" + strings.ToLower(model)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, contextFallbackCandidate{provider: provider, model: model})
	}
	requestResult, aliasKeys := modelAliasLookupCandidates(logicalModel)
	for _, lookup := range []string{logicalModel, requestResult.ModelName} {
		lookup = strings.TrimSpace(lookup)
		if lookup == "" {
			continue
		}
		for _, provider := range registry.GetGlobalRegistry().GetModelProviders(lookup) {
			add(provider, logicalModel)
		}
	}
	if raw := m.oauthModelAlias.Load(); raw != nil {
		if table, ok := raw.(*oauthModelAliasTable); ok && table != nil && len(table.reverse) > 0 {
			channels := make([]string, 0, len(table.reverse))
			for channel := range table.reverse {
				if !strings.EqualFold(strings.TrimSpace(channel), "codex") {
					channels = append(channels, channel)
				}
			}
			sort.Strings(channels)
			for _, channel := range channels {
				rev := table.reverse[channel]
				for _, key := range aliasKeys {
					key = strings.ToLower(strings.TrimSpace(key))
					if key == "" {
						continue
					}
					entry, exists := rev[key]
					if !exists || strings.TrimSpace(entry.upstreamModel) == "" {
						continue
					}
					add(channel, preserveResolvedModelSuffix(entry.upstreamModel, requestResult))
					break
				}
			}
		}
	}
	// ponytail: no explicit-fallback tier; exact model or configured alias or
	// nothing. Arbitrary substitution would silently change model behavior.
	return candidates
}

// contextFallbackCapacityOK requires advertised context >= requestTokens.
// Unknown (<=0) advertised context is ineligible when the request size is known.
func (m *Manager) contextFallbackCapacityOK(model, provider string, requestTokens int) bool {
	if requestTokens <= 0 {
		return true
	}
	capacity := 0
	for _, lookup := range []string{model, thinking.ParseSuffix(model).ModelName} {
		lookup = strings.TrimSpace(lookup)
		if lookup == "" {
			continue
		}
		if info := registry.GetGlobalRegistry().GetModelInfo(lookup, provider); info != nil {
			if info.ContextLength > capacity {
				capacity = info.ContextLength
			}
			if info.MaxContextLength > capacity {
				capacity = info.MaxContextLength
			}
		}
	}
	return capacity > 0 && capacity >= requestTokens
}

// contextFallbackAuthReady requires a registered executor plus an enabled,
// healthy credential with usable auth material for provider/model.
func (m *Manager) contextFallbackAuthReady(provider, model string) bool {
	if m == nil {
		return false
	}
	now := time.Now()
	m.mu.RLock()
	defer m.mu.RUnlock()
	executorReady := false
	for key := range m.executors {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(provider)) {
			executorReady = true
			break
		}
	}
	if !executorReady {
		return false
	}
	modelKey := canonicalModelKey(model)
	for _, auth := range m.auths {
		if auth == nil || auth.Disabled || auth.Status == StatusDisabled {
			continue
		}
		if !strings.EqualFold(executorKeyFromAuth(auth), strings.TrimSpace(provider)) &&
			!strings.EqualFold(strings.TrimSpace(auth.Provider), strings.TrimSpace(provider)) {
			continue
		}
		if auth.AuthKind() == "" && !authHasOAuthMetadata(auth) {
			continue
		}
		if blocked, _, _ := availabilityBlock(auth.Unavailable, auth.Quota.Exceeded, auth.NextRetryAfter, auth.Quota.NextRecoverAt, now); blocked {
			continue
		}
		blocked := false
		for _, key := range []string{model, modelKey} {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			state := auth.ModelStates[key]
			if state == nil {
				continue
			}
			if state.Status == StatusDisabled {
				blocked = true
				break
			}
			if stateBlocked, _, _ := availabilityBlock(state.Unavailable, state.Quota.Exceeded, state.NextRetryAfter, state.Quota.NextRecoverAt, now); stateBlocked {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		return true
	}
	return false
}

// codexContextLimitTerminalError is the terminal no-candidate error: the Codex
// context failed and no configured ai-provider can serve the request size.
func codexContextLimitTerminalError(requestTokens int) *Error {
	if requestTokens > 0 {
		return &Error{
			Code:       "context_window_exceeded",
			Message:    fmt.Sprintf("Codex context limit exceeded at %d tokens and no configured ai-provider can handle the request context", requestTokens),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return &Error{
		Code:       "context_window_exceeded",
		Message:    "Codex context limit exceeded and no configured ai-provider can handle the request context",
		HTTPStatus: http.StatusBadRequest,
	}
}

// codexContextFallbackCause preserves the original failure while guaranteeing
// errors.Is(err, ErrContextWindowExceeded): raw causes admitted via the legacy
// substring matcher gain a synthesized typed carrier (counts included), so the
// terminal error satisfies errors.Is for typed and raw causes alike.
func codexContextFallbackCause(plan *codexContextFallbackPlan) error {
	if plan == nil || plan.cause == nil {
		return nil
	}
	if errors.Is(plan.cause, ErrContextWindowExceeded) {
		return plan.cause
	}
	return errors.Join(plan.cause, &ContextWindowExceededError{
		MaxContext:    plan.maxContext,
		RequestTokens: plan.requestTokens,
		Provider:      "codex",
		Model:         plan.logicalModel,
		Raw:           plan.cause.Error(),
	})
}

type codexContextFallbackPlan struct {
	logicalModel  string
	requestTokens int
	maxContext    int
	cause         error
	exclude       map[string]bool
	failedRoute   string
	lastErr       error
}

// newCodexContextFallbackPlan admits only terminal Codex context failures with
// a resolvable logical model; otherwise the caller continues its chain.
func (m *Manager) newCodexContextFallbackPlan(normalized []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, cause error) (*codexContextFallbackPlan, bool) {
	if !hasCodexProvider(normalized) || !isCodexContextWindowExceeded(cause) {
		return nil, false
	}
	logicalModel := authSelectionModelFromOptions(opts, req.Model)
	if strings.TrimSpace(logicalModel) == "" {
		return nil, false
	}
	tokens, maxContext := contextFallbackRequestTokens(cause, req, opts)
	return &codexContextFallbackPlan{
		logicalModel:  logicalModel,
		requestTokens: tokens,
		maxContext:    maxContext,
		cause:         cause,
		exclude:       contextFallbackExcludedRoutes(opts),
	}, true
}

// nextCodexContextFallbackRoute selects the next untried eligible route,
// marking it attempted so a provider context-failure advances instead of
// looping. terminal is the best error once no route remains: the last
// provider context error, or the no-candidate error wrapping the original
// cause (errors.Is ErrContextWindowExceeded holds; MaxContext/RequestTokens
// ride the *ContextWindowExceededError cause, synthesized when raw).
func (m *Manager) nextCodexContextFallbackRoute(plan *codexContextFallbackPlan) (provider, model string, terminal error, ok bool) {
	provider, model, ok = m.SelectContextFallback(plan.logicalModel, plan.requestTokens, plan.exclude)
	if !ok {
		if plan.lastErr != nil {
			return "", "", plan.lastErr, false
		}
		// ponytail: no-candidate terminal keeps the upstream *Error as base so
		// status/body stay client-compatible; typed context metadata rides the
		// cause chain for errors.Is routing. Non-*Error causes keep the
		// synthesized terminal below.
		var upstreamErr *Error
		if errors.As(plan.cause, &upstreamErr) && upstreamErr != nil {
			return "", "", WithCause(upstreamErr, codexContextFallbackCause(plan)), false
		}
		return "", "", WithCause(codexContextLimitTerminalError(plan.requestTokens), codexContextFallbackCause(plan)), false
	}
	plan.exclude[contextFallbackRouteKey(provider, model)] = true
	return provider, model, nil, true
}

// routeCodexContextFallbackRequest swaps only the model; the canonical
// original payload rides along so the fallback adapter translates from
// opts.OriginalRequest, never from a Codex-mutated body.
func routeCodexContextFallbackRequest(req cliproxyexecutor.Request, opts cliproxyexecutor.Options, provider, model string) (cliproxyexecutor.Request, cliproxyexecutor.Options) {
	fbReq := req
	fbReq.Model = model
	return fbReq, withContextFallbackAttempted(opts, provider, model)
}

// logCodexContextFallback emits the one concise event per diversion outcome:
// model/max/requestTokens plus fallback route or terminal reason. Counts and
// route names only, never secrets or bodies.
func logCodexContextFallback(ctx context.Context, plan *codexContextFallbackPlan, provider, model string, success bool) {
	fields := log.Fields{
		"model":          plan.logicalModel,
		"max_context":    plan.maxContext,
		"request_tokens": plan.requestTokens,
	}
	if success {
		fields["fallback_provider"] = provider
		fields["fallback_model"] = model
		logEntryWithRequestID(ctx).WithFields(fields).Info(contextFallbackLogMessage)
		return
	}
	if provider != "" {
		fields["failed_route"] = provider + "/" + model
	}
	if plan.failedRoute != "" && provider == "" {
		fields["failed_route"] = plan.failedRoute
	}
	if plan.lastErr != nil {
		fields["reason"] = "ai-provider context limit exceeded"
	} else {
		fields["reason"] = "no eligible ai-provider"
	}
	logEntryWithRequestID(ctx).WithFields(fields).Warn(contextFallbackLogMessage)
}

// tryCodexContextProviderFallbackExecute reroutes a terminal Codex context
// failure to context-eligible ai-providers. handled=false means not
// applicable: caller continues the existing fallback chain.
func (m *Manager) tryCodexContextProviderFallbackExecute(ctx context.Context, normalized []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, cause error) (cliproxyexecutor.Response, bool, error) {
	plan, applicable := m.newCodexContextFallbackPlan(normalized, req, opts, cause)
	if !applicable {
		return cliproxyexecutor.Response{}, false, nil
	}
	for {
		provider, model, terminal, ok := m.nextCodexContextFallbackRoute(plan)
		if !ok {
			logCodexContextFallback(ctx, plan, "", "", false)
			return cliproxyexecutor.Response{}, true, terminal
		}
		fbReq, fbOpts := routeCodexContextFallbackRequest(req, opts, provider, model)
		resp, err := m.runMixedRetry(ctx, []string{provider}, fbReq, fbOpts)
		if err == nil {
			logCodexContextFallback(ctx, plan, provider, model, true)
			return resp, true, nil
		}
		plan.failedRoute = provider + "/" + model
		if !isCodexContextWindowExceeded(err) {
			logCodexContextFallback(ctx, plan, provider, model, false)
			return cliproxyexecutor.Response{}, true, err
		}
		plan.lastErr = err
	}
}

// tryCodexContextProviderFallbackExecuteStream mirrors the non-stream hook for
// pre-stream Codex context failures. Terminal outcomes ride a stream error
// result so SSE consumers still receive the message as chunks.
func (m *Manager) tryCodexContextProviderFallbackExecuteStream(ctx context.Context, normalized []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, cause error) (*cliproxyexecutor.StreamResult, bool, error) {
	plan, applicable := m.newCodexContextFallbackPlan(normalized, req, opts, cause)
	if !applicable {
		return nil, false, nil
	}
	for {
		provider, model, terminal, ok := m.nextCodexContextFallbackRoute(plan)
		if !ok {
			logCodexContextFallback(ctx, plan, "", "", false)
			return streamErrorResult(nil, terminal), true, nil
		}
		fbReq, fbOpts := routeCodexContextFallbackRequest(req, opts, provider, model)
		result, err := m.runStreamMixedRetry(ctx, []string{provider}, fbReq, fbOpts)
		if err == nil {
			logCodexContextFallback(ctx, plan, provider, model, true)
			return result, true, nil
		}
		plan.failedRoute = provider + "/" + model
		if !isCodexContextWindowExceeded(err) {
			logCodexContextFallback(ctx, plan, provider, model, false)
			return nil, true, err
		}
		plan.lastErr = err
	}
}
