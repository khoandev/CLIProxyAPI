package executor

import (
	"context"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/sjson"
)

// refreshCodexAuthForTokenInvalidatedRetry refreshes a Codex credential before a
// token_invalidated retry. It is a variable so tests can stub the refresh.
var refreshCodexAuthForTokenInvalidatedRetry = func(ctx context.Context, e *CodexExecutor, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	return e.Refresh(ctx, auth)
}

// isCodexTokenInvalidatedResponse reports whether an upstream 401 means the access
// token was invalidated server-side, which a refresh can recover from.
func isCodexTokenInvalidatedResponse(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized {
		return false
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "token_invalidated") || strings.Contains(lower, "authentication token has been invalidated")
}

// replayCodexRequest rebuilds and issues a Codex request for the given body, then
// records it in the upstream log. It closes current before sending the replay.
func (e *CodexExecutor) replayCodexRequest(
	ctx context.Context,
	auth *cliproxyauth.Auth,
	from sdktranslator.Format,
	url string,
	req cliproxyexecutor.Request,
	opts cliproxyexecutor.Options,
	body []byte,
	httpClient *http.Client,
	current *http.Response,
) (*http.Response, error) {
	apiKey, _ := codexCreds(auth)
	retryReq, retryUpstreamBody, retryReqErr := e.cacheHelper(ctx, from, url, req, body, opts.Headers)
	if retryReqErr != nil {
		return nil, retryReqErr
	}
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	applyCodexHeaders(retryReq, auth, apiKey, true, e.cfg, opts.Headers)
	applyCodexRoutingHint(ctx, retryReq.Header, auth, baseModel, retryUpstreamBody, opts.Headers)
	applyModelHeaderOverrides(retryReq.Header, baseModel)
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   retryReq.Header.Clone(),
		Body:      retryUpstreamBody,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})
	if current != nil {
		if errClose := current.Body.Close(); errClose != nil {
			log.Errorf("codex executor: close response body error: %v", errClose)
		}
	}
	retryResp, errDo := httpClient.Do(retryReq)
	if errDo != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errDo)
		return nil, errDo
	}
	helps.RecordAPIResponseMetadata(ctx, e.cfg, retryResp.StatusCode, retryResp.Header.Clone())
	return retryResp, nil
}

// retryAfterCodexTokenInvalidated refreshes the credential and replays the request
// once after an upstream token_invalidated 401. It closes current before issuing the
// replay, and reports retried=false when the refresh did not yield a usable auth.
func (e *CodexExecutor) retryAfterCodexTokenInvalidated(
	ctx context.Context,
	auth *cliproxyauth.Auth,
	from sdktranslator.Format,
	pathSuffix string,
	req cliproxyexecutor.Request,
	opts cliproxyexecutor.Options,
	body []byte,
	httpClient *http.Client,
	current *http.Response,
) (*http.Response, *cliproxyauth.Auth, bool, error) {
	refreshedAuth, refreshErr := refreshCodexAuthForTokenInvalidatedRetry(ctx, e, auth)
	if refreshErr != nil {
		helps.LogWithRequestID(ctx).Debugf("codex token_invalidated refresh retry failed: %v", refreshErr)
		return nil, auth, false, nil
	}
	if refreshedAuth == nil {
		return nil, auth, false, nil
	}
	auth = refreshedAuth
	_, baseURL := codexCreds(auth)
	if baseURL == "" {
		baseURL = "https://chatgpt.com/backend-api/codex"
	}
	url := strings.TrimSuffix(baseURL, "/") + pathSuffix
	retryResp, errDo := e.replayCodexRequest(ctx, auth, from, url, req, opts, body, httpClient, current)
	if errDo != nil {
		return nil, auth, false, errDo
	}
	return retryResp, auth, true, nil
}

// applyCodexFastServiceTier applies fast-service-tier policy at the very last
// moment before sending, so it survives all prior payload processing.
//
// When fast-service-tier is enabled, service_tier is forced to "priority".
// When fast-service-tier is disabled, any service_tier the caller sent is
// stripped so that fast-mode requests never reach the upstream.
func applyCodexFastServiceTier(cfg *config.Config, body []byte) []byte {
	if cfg == nil || len(body) == 0 {
		return body
	}
	if cfg.FastServiceTier {
		body, _ = sjson.SetBytes(body, "service_tier", "priority")
		return body
	}
	// fast-service-tier=false: remove any service_tier the caller supplied.
	body, _ = sjson.DeleteBytes(body, "service_tier")
	return body
}
