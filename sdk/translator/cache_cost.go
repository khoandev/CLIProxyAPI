package translator

import (
	"bytes"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/cachecost"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// cacheUsagePaths maps a client format to the usage JSON paths describing its
// total input tokens and its cache-read tokens.
type cacheUsagePaths struct {
	inputPath string
	cachePath string
}

var cacheUsagePathsByFormat = map[Format]cacheUsagePaths{
	FormatOpenAI:         {inputPath: "usage.prompt_tokens", cachePath: "usage.prompt_tokens_details.cached_tokens"},
	FormatOpenAIResponse: {inputPath: "usage.input_tokens", cachePath: "usage.input_tokens_details.cached_tokens"},
	FormatGemini:         {inputPath: "usageMetadata.promptTokenCount", cachePath: "usageMetadata.cachedContentTokenCount"},
	FormatClaude:         {inputPath: "usage.input_tokens", cachePath: "usage.cache_read_input_tokens"},
}

// applyCacheHitCostRatio rescales cache-read tokens in an already translated
// client-facing payload so that they keep only the configured share of the cache
// price; the remainder is billed as ordinary input tokens. Every total is left
// unchanged, so clients still see a consistent prompt token count.
// It returns the payload untouched when the feature is disabled, when the client
// format carries no cache-read field, or when no cache tokens are reported.
func applyCacheHitCostRatio(body []byte, to Format) []byte {
	ratio := cachecost.Ratio()
	if ratio >= 1 || len(body) == 0 {
		return body
	}
	paths, ok := cacheUsagePathsByFormat[to]
	if !ok {
		return body
	}
	return redistributeCacheTokensInPayload(body, paths, ratio)
}

// applyCacheHitCostRatioToStream rescales cache-read tokens in every streamed chunk.
func applyCacheHitCostRatioToStream(chunks [][]byte, to Format) [][]byte {
	ratio := cachecost.Ratio()
	if ratio >= 1 {
		return chunks
	}
	paths, ok := cacheUsagePathsByFormat[to]
	if !ok {
		return chunks
	}
	for i, chunk := range chunks {
		updated := redistributeCacheTokensInPayload(chunk, paths, ratio)
		if !bytes.Equal(updated, chunk) {
			chunks[i] = updated
		}
	}
	return chunks
}

// redistributeCacheTokensInPayload rewrites a single JSON object, leaving the
// surrounding SSE framing untouched.
func redistributeCacheTokensInPayload(body []byte, paths cacheUsagePaths, ratio float64) []byte {
	input := gjson.GetBytes(body, paths.inputPath)
	cached := gjson.GetBytes(body, paths.cachePath)
	if !cached.Exists() || !input.Exists() || cached.Int() <= 0 || input.Int() <= 0 {
		return body
	}
	newCached := cachecost.ScaledCacheTokens(cached.Int(), ratio)
	if newCached == cached.Int() {
		return body
	}
	updated, err := sjson.SetBytes(body, paths.cachePath, newCached)
	if err != nil {
		return body
	}
	return updated
}
