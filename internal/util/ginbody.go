package util

import (
	"bytes"
	"errors"
	"io"

	"github.com/gin-gonic/gin"
)

// SharedRequestBodyContextKey caches the raw request body on the gin context.
const SharedRequestBodyContextKey = "cpa.requestBody.shared"

// DecodedRequestBodyContextKey caches the request body after Content-Encoding
// decoding on the gin context.
const DecodedRequestBodyContextKey = "cpa.requestBody.decoded"

// CachedDecodedRequestBody returns the body that was already decoded for this
// request. The logging middleware decodes before the handler runs, so sharing
// the result keeps a compressed body from being decompressed twice.
//
// The returned slice is shared, so it must not be modified in place.
func CachedDecodedRequestBody(c *gin.Context) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	cached, ok := c.Get(DecodedRequestBodyContextKey)
	if !ok {
		return nil, false
	}
	body, ok := cached.([]byte)
	if !ok || len(body) == 0 {
		return nil, false
	}
	return body, true
}

// SetDecodedRequestBody caches the decoded request body for this request.
// Callers must not cache a partial or failed decode.
func SetDecodedRequestBody(c *gin.Context, body []byte) {
	if c == nil || len(body) == 0 {
		return
	}
	c.Set(DecodedRequestBodyContextKey, body)
}

// ErrNilRequestBody is returned when the request carries no body reader.
var ErrNilRequestBody = errors.New("cannot read nil body")

// SharedRequestBody returns the raw request body, reading it at most once per
// request and caching the bytes on the gin context.
//
// The middleware and the handler used to read the body separately, so every
// request paid for several full copies of the payload. The returned slice is
// shared between the callers, so it must not be modified in place.
func SharedRequestBody(c *gin.Context) ([]byte, error) {
	if c == nil {
		return nil, ErrNilRequestBody
	}
	if cached, ok := c.Get(SharedRequestBodyContextKey); ok {
		body, ok := cached.([]byte)
		if !ok {
			return nil, ErrNilRequestBody
		}
		return body, nil
	}
	if c.Request == nil || c.Request.Body == nil {
		return nil, ErrNilRequestBody
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		// A failed read is not cached, otherwise the next reader would see a
		// truncated body instead of the read error.
		return nil, err
	}
	// Restore the body so the later readers (multipart upload, deferred
	// capture tee) still see the payload.
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Set(SharedRequestBodyContextKey, body)
	return body, nil
}
