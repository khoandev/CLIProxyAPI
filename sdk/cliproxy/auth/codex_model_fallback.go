package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/modelversion"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// hasCodexProvider reports whether any of the given providers is the Codex OAuth provider.
func hasCodexProvider(providers []string) bool {
	for _, p := range providers {
		if strings.EqualFold(strings.TrimSpace(p), "codex") {
			return true
		}
	}
	return false
}

var codexContextFallbacks = map[string]string{
	"gpt-5.6-sol":   "gpt-5.5",
	"gpt-5.6-luna":  "gpt-5.4",
	"gpt-5.6-terra": "gpt-5.4",
	"gpt-5.5":       "gpt-5.4",
}

// ponytail: max-context fallback is intentionally fixed and stops at gpt-5.4; add transitions only when product policy changes.

func nextCodexContextFallbackModel(requested string) (string, bool) {
	parsed := thinking.ParseSuffix(requested)
	target, applies := codexContextFallbacks[strings.ToLower(strings.TrimSpace(parsed.ModelName))]
	if !applies {
		return "", false
	}
	candidate := modelversion.Next(requested, []string{"codex"})
	if candidate == "" {
		return "", true
	}
	if got := strings.ToLower(strings.TrimSpace(thinking.ParseSuffix(candidate).ModelName)); got != target {
		return "", true
	}
	return candidate, true
}

// buildCodexFallbackRequestForModel constructs a fallback request for an explicit model.
func buildCodexFallbackRequestForModel(req cliproxyexecutor.Request, opts cliproxyexecutor.Options, fallbackModel string) (cliproxyexecutor.Request, cliproxyexecutor.Options) {
	fbReq := req
	fbReq.Model = fallbackModel

	fbMeta := make(map[string]any, len(opts.Metadata)+2)
	for k, v := range opts.Metadata {
		fbMeta[k] = v
	}
	displayModel := thinking.ParseSuffix(req.Model).ModelName
	if existing, ok := fbMeta[cliproxyexecutor.CodexFallbackDisplayModelMetadataKey].(string); ok && strings.TrimSpace(existing) != "" {
		displayModel = existing
	}
	fbMeta[cliproxyexecutor.CodexFallbackDisplayModelMetadataKey] = displayModel
	fbMeta[cliproxyexecutor.RequestedModelMetadataKey] = thinking.ParseSuffix(fallbackModel).ModelName

	fbOpts := opts
	fbOpts.Metadata = fbMeta
	return fbReq, fbOpts
}

// buildCodexFallbackRequest constructs a fallback request substituting the fallback model
// and recording the original model name for transparent response rewriting.
// Returns the fallback request, fallback opts, and ok=true if a registry-backed downgrade exists.
func buildCodexFallbackRequest(req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Request, cliproxyexecutor.Options, bool) {
	fbModel := modelversion.Next(req.Model, []string{"codex"})
	if fbModel == "" {
		return req, opts, false
	}
	fbReq, fbOpts := buildCodexFallbackRequestForModel(req, opts, fbModel)
	return fbReq, fbOpts, true
}

// tryCodexModelFallbackExecute attempts a non-streaming execution with the Codex model fallback.
// It runs the full retry cycle for the fallback model.
// Returns (response, true) on success, (zero, false) if no fallback applies or all attempts fail.
func (m *Manager) tryCodexModelFallbackExecute(ctx context.Context, normalized []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, cause error) (cliproxyexecutor.Response, bool) {
	if !hasCodexProvider(normalized) {
		return cliproxyexecutor.Response{}, false
	}
	if isContextWindowExceededError(cause) {
		currentBase := strings.ToLower(strings.TrimSpace(thinking.ParseSuffix(req.Model).ModelName))
		if currentBase == "gpt-5.4" {
			return cliproxyexecutor.Response{}, false
		}
		currentReq, currentOpts := req, opts
		if _, applies := nextCodexContextFallbackModel(currentReq.Model); applies {
			for {
				fbModel, _ := nextCodexContextFallbackModel(currentReq.Model)
				if fbModel == "" {
					return cliproxyexecutor.Response{}, false
				}
				fbReq, fbOpts := buildCodexFallbackRequestForModel(currentReq, currentOpts, fbModel)
				resp, err := m.runMixedRetry(ctx, normalized, fbReq, fbOpts)
				if err == nil {
					return resp, true
				}
				if !isContextWindowExceededError(err) {
					return cliproxyexecutor.Response{}, false
				}
				currentReq, currentOpts = fbReq, fbOpts
			}
		}
		return cliproxyexecutor.Response{}, false
	}
	fbReq, fbOpts, ok := buildCodexFallbackRequest(req, opts)
	if !ok {
		return cliproxyexecutor.Response{}, false
	}
	resp, err := m.runMixedRetry(ctx, normalized, fbReq, fbOpts)
	if err != nil {
		return cliproxyexecutor.Response{}, false
	}
	return resp, true
}

// tryCodexModelFallbackExecuteStream attempts a streaming execution with the Codex model fallback.
// It runs the full retry cycle for the fallback model.
// Returns (result, true) on success, (nil, false) if no fallback applies or all attempts fail.
func (m *Manager) tryCodexModelFallbackExecuteStream(ctx context.Context, normalized []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, cause error) (*cliproxyexecutor.StreamResult, bool) {
	if !hasCodexProvider(normalized) {
		return nil, false
	}
	if isContextWindowExceededError(cause) {
		currentReq, currentOpts := req, opts
		currentBase := strings.ToLower(strings.TrimSpace(thinking.ParseSuffix(currentReq.Model).ModelName))
		if currentBase == "gpt-5.4" {
			var bootstrapErr *streamBootstrapError
			if errors.As(cause, &bootstrapErr) && bootstrapErr != nil {
				return streamErrorResult(bootstrapErr.Headers(), bootstrapErr.cause), true
			}
			return streamErrorResult(nil, cause), true
		}
		if _, applies := nextCodexContextFallbackModel(currentReq.Model); applies {
			lastErr := cause
			for {
				fbModel, nextApplies := nextCodexContextFallbackModel(currentReq.Model)
				if !nextApplies || fbModel == "" {
					var bootstrapErr *streamBootstrapError
					if errors.As(lastErr, &bootstrapErr) && bootstrapErr != nil {
						return streamErrorResult(bootstrapErr.Headers(), bootstrapErr.cause), true
					}
					return streamErrorResult(nil, lastErr), true
				}
				fbReq, fbOpts := buildCodexFallbackRequestForModel(currentReq, currentOpts, fbModel)
				result, err := m.runStreamMixedRetry(ctx, normalized, fbReq, fbOpts)
				if err == nil {
					return result, true
				}
				if !isContextWindowExceededError(err) {
					return nil, false
				}
				lastErr = err
				currentReq, currentOpts = fbReq, fbOpts
			}
		}
		var bootstrapErr *streamBootstrapError
		if errors.As(cause, &bootstrapErr) && bootstrapErr != nil {
			return streamErrorResult(bootstrapErr.Headers(), bootstrapErr.cause), true
		}
		return streamErrorResult(nil, cause), true
	}
	fbReq, fbOpts, ok := buildCodexFallbackRequest(req, opts)
	if !ok {
		return nil, false
	}
	result, err := m.runStreamMixedRetry(ctx, normalized, fbReq, fbOpts)
	if err != nil {
		return nil, false
	}
	return result, true
}
