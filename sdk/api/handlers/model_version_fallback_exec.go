package handlers

import (
	"context"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/modelversion"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

const maxVersionFallbackHops = 16

// QuotaExceededBehavior mirrors config.QuotaExceeded fields used by handler-level model failover.
type QuotaExceededBehavior struct {
	SwitchPreviewModel bool
}

func (h *BaseAPIHandler) modelVersionFallbackEnabled() bool {
	return h != nil && h.quotaExceeded.SwitchPreviewModel
}

func (h *BaseAPIHandler) shouldAttemptModelVersionFallback(err error) bool {
	if h == nil || !h.modelVersionFallbackEnabled() {
		return false
	}
	return coreauth.ModelSupportError(err) || coreauth.ModelQuotaOrCapacityError(err)
}

func (h *BaseAPIHandler) shouldAttemptRoutingModelVersionFallback(errMsg *interfaces.ErrorMessage) bool {
	if h == nil || !h.modelVersionFallbackEnabled() || errMsg == nil {
		return false
	}
	// Image-only models reject non-image endpoints; downgrading the model id does not help.
	if errMsg.StatusCode == http.StatusServiceUnavailable {
		return false
	}
	return true
}

// modelversionFallbackState tracks the lazily-snapshotted downgrade chain for
// a single fallback sequence. The chain is computed at most once, on the
// first hop that actually needs to fall back, so a first-hop success never
// touches modelversion.Chain at all.
type modelversionFallbackState struct {
	chain []string
	index int
	ready bool
}

// next returns the next model to try, snapshotting the chain via
// modelversion.Chain exactly once (on first use). Returns "" once the chain
// is exhausted, matching the previous per-hop Next() exhaustion behavior.
func (s *modelversionFallbackState) next(requested string, providers []string) string {
	if !s.ready {
		s.chain = modelversion.Chain(requested, providers)
		s.ready = true
	}
	if s.index >= len(s.chain) {
		return ""
	}
	next := s.chain[s.index]
	s.index++
	return next
}

func (h *BaseAPIHandler) executeWithAuthManagerFormatsWithVersionFallback(
	ctx context.Context, entryProtocol, exitProtocol, modelName string,
	rawJSON []byte, alt string, allowImageModel bool, execOptions modelExecutionOptions,
	providers []string,
) ([]byte, http.Header, *interfaces.ErrorMessage) {
	originalRequestedModel := modelName
	currentModel := modelName
	currentPayload := rawJSON
	var lastErr *interfaces.ErrorMessage
	var fallback modelversionFallbackState

	for range maxVersionFallbackHops {
		body, headers, errMsg := h.executeWithAuthManagerFormatsOnce(
			ctx, entryProtocol, exitProtocol, currentModel, currentPayload, alt, allowImageModel, execOptions,
		)
		if errMsg == nil {
			if currentModel == originalRequestedModel {
				return body, headers, nil
			}
			return restoreOriginalModelInBody(body, originalRequestedModel), headers, nil
		}
		lastErr = errMsg
		if !h.shouldAttemptRoutingModelVersionFallback(errMsg) && !h.shouldAttemptModelVersionFallback(errMsg.Error) {
			return nil, headers, errMsg
		}
		next := fallback.next(originalRequestedModel, providers)
		if next == "" {
			return nil, headers, errMsg
		}
		currentModel = next
		currentPayload = withFallbackModelInPayload(rawJSON, next)
	}
	return nil, nil, lastErr
}

func (h *BaseAPIHandler) executeCountWithAuthManagerWithVersionFallback(
	ctx context.Context, handlerType, modelName string,
	rawJSON []byte, alt string, execOptions modelExecutionOptions,
	providers []string,
) ([]byte, http.Header, *interfaces.ErrorMessage) {
	originalRequestedModel := modelName
	currentModel := modelName
	currentPayload := rawJSON
	var lastErr *interfaces.ErrorMessage
	var fallback modelversionFallbackState

	for range maxVersionFallbackHops {
		body, headers, errMsg := h.executeCountWithAuthManagerOnce(
			ctx, handlerType, currentModel, currentPayload, alt, execOptions,
		)
		if errMsg == nil {
			if currentModel == originalRequestedModel {
				return body, headers, nil
			}
			return restoreOriginalModelInBody(body, originalRequestedModel), headers, nil
		}
		lastErr = errMsg
		if !h.shouldAttemptRoutingModelVersionFallback(errMsg) && !h.shouldAttemptModelVersionFallback(errMsg.Error) {
			return nil, headers, errMsg
		}
		next := fallback.next(originalRequestedModel, providers)
		if next == "" {
			return nil, headers, errMsg
		}
		currentModel = next
		currentPayload = withFallbackModelInPayload(rawJSON, next)
	}
	return nil, nil, lastErr
}

func (h *BaseAPIHandler) executeStreamWithAuthManagerFormatsWithVersionFallback(
	ctx context.Context, entryProtocol, exitProtocol, modelName string,
	rawJSON []byte, alt string, allowImageModel bool, execOptions modelExecutionOptions,
	providers []string,
) (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
	originalRequestedModel := modelName
	currentModel := modelName
	currentPayload := rawJSON
	var fallback modelversionFallbackState

	for range maxVersionFallbackHops {
		dataChan, headers, errChan := h.executeStreamWithAuthManagerFormatsOnce(
			ctx, entryProtocol, exitProtocol, currentModel, currentPayload, alt, allowImageModel, execOptions,
		)
		if dataChan != nil {
			if currentModel == originalRequestedModel {
				return dataChan, headers, errChan
			}
			wrapped := make(chan []byte)
			go func(displayModel string, in <-chan []byte) {
				defer close(wrapped)
				for chunk := range in {
					if len(chunk) > 0 {
						chunk = restoreOriginalModelInChunk(chunk, displayModel)
					}
					if ctx == nil {
						wrapped <- chunk
						continue
					}
					select {
					case <-ctx.Done():
						return
					case wrapped <- chunk:
					}
				}
			}(originalRequestedModel, dataChan)
			return wrapped, headers, errChan
		}
		var errMsg *interfaces.ErrorMessage
		select {
		case errMsg = <-errChan:
		default:
		}
		if errMsg == nil {
			return nil, headers, errChan
		}
		if !h.shouldAttemptRoutingModelVersionFallback(errMsg) && !h.shouldAttemptModelVersionFallback(errMsg.Error) {
			out := make(chan *interfaces.ErrorMessage, 1)
			out <- errMsg
			close(out)
			return nil, headers, out
		}
		next := fallback.next(originalRequestedModel, providers)
		if next == "" {
			out := make(chan *interfaces.ErrorMessage, 1)
			out <- errMsg
			close(out)
			return nil, headers, out
		}
		currentModel = next
		currentPayload = withFallbackModelInPayload(rawJSON, next)
	}
	errChan := make(chan *interfaces.ErrorMessage, 1)
	errChan <- &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: http.ErrHandlerTimeout}
	close(errChan)
	return nil, nil, errChan
}
