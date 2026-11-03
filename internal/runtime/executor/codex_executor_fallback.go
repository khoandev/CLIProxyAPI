package executor

import (
	"github.com/MoeForge/moego/ojson"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func codexFallbackDisplayModel(opts cliproxyexecutor.Options) string {
	if v, ok := opts.Metadata[cliproxyexecutor.CodexFallbackDisplayModelMetadataKey].(string); ok {
		return v
	}
	return ""
}

// rewriteCodexResponseModel rewrites the "model" and "response.model" fields in a
// Codex response payload to displayModel, making the model fallback transparent to clients.
func rewriteCodexResponseModel(payload []byte, displayModel string) []byte {
	if displayModel == "" || len(payload) == 0 {
		return payload
	}
	out, err := ojson.SetBytes(payload, "model", displayModel)
	if err != nil {
		return payload
	}
	if ojson.GetBytesNoCopy(out, "response.model").Exists() {
		if patched, errPatch := ojson.SetBytes(out, "response.model", displayModel); errPatch == nil {
			out = patched
		}
	}
	return out
}
