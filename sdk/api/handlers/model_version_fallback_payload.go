package handlers

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// withFallbackModelInPayload rewrites the "model" field so a retry targets the
// fallback model. It leaves the payload untouched when no model field is present.
func withFallbackModelInPayload(rawJSON []byte, fallbackModel string) []byte {
	if len(rawJSON) == 0 {
		return rawJSON
	}
	if !gjson.GetBytes(rawJSON, "model").Exists() {
		return rawJSON
	}
	updated, err := sjson.SetBytes(rawJSON, "model", fallbackModel)
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
	if !gjson.GetBytes(body, "model").Exists() {
		return body
	}
	updated, err := sjson.SetBytes(body, "model", originalModel)
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
	if !gjson.GetBytes(chunk, "model").Exists() {
		return chunk
	}
	updated, err := sjson.SetBytes(chunk, "model", originalModel)
	if err != nil {
		return chunk
	}
	return updated
}
