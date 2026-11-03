package handlers

import (
	"bytes"

	"github.com/MoeForge/moego/ojson"
)

// withFallbackModelInPayload rewrites the "model" field so a retry targets the
// fallback model. It leaves the payload untouched when no model field is present.
func withFallbackModelInPayload(rawJSON []byte, fallbackModel string) []byte {
	if len(rawJSON) == 0 {
		return rawJSON
	}
	if !ojson.GetBytesNoCopy(rawJSON, "model").Exists() {
		return rawJSON
	}
	updated, err := ojson.SetBytes(rawJSON, "model", fallbackModel)
	if err != nil {
		return rawJSON
	}
	return updated
}

// restoreOriginalModelInBody puts the originally requested model name back into a
// non-streaming response body so the fallback stays transparent to the client.
func restoreOriginalModelInBody(body []byte, originalModel string) []byte {
	if len(body) == 0 || originalModel == "" {
		return body
	}
	if !ojson.GetBytesNoCopy(body, "model").Exists() {
		return body
	}
	updated, err := ojson.SetBytes(body, "model", originalModel)
	if err != nil {
		return body
	}
	return updated
}

// restoreOriginalModelInChunk puts the originally requested model name back into a
// streaming chunk so the fallback stays transparent to the client.
func restoreOriginalModelInChunk(chunk []byte, originalModel string) []byte {
	if len(chunk) == 0 || originalModel == "" {
		return chunk
	}
	// Streaming chunks arrive framed as "data: <json>\n\n". The JSON editor
	// treats the whole frame as a scalar, so rewrite only the JSON body and
	// reattach the frame around it.
	if bytes.HasPrefix(chunk, []byte("data:")) {
		frameHead := chunk[:len("data:")]
		rest := chunk[len("data:"):]
		body := bytes.TrimLeft(rest, " \t")
		frameLead := rest[:len(rest)-len(body)]
		core := bytes.TrimRight(body, "\r\n")
		frameTail := body[len(core):]
		if !ojson.GetBytesNoCopy(core, "model").Exists() {
			return chunk
		}
		updated, err := ojson.SetBytes(core, "model", originalModel)
		if err != nil {
			return chunk
		}
		out := make([]byte, 0, len(chunk)+len(updated)-len(core))
		out = append(out, frameHead...)
		out = append(out, frameLead...)
		out = append(out, updated...)
		out = append(out, frameTail...)
		return out
	}
	if !ojson.GetBytesNoCopy(chunk, "model").Exists() {
		return chunk
	}
	updated, err := ojson.SetBytes(chunk, "model", originalModel)
	if err != nil {
		return chunk
	}
	return updated
}
