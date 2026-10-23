// Package thinking provides unified thinking configuration processing.
package thinking

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// StripThinkingConfig removes thinking configuration fields from request body.
//
// This function is used when a model doesn't support thinking but the request
// contains thinking configuration. The configuration is silently removed to
// prevent upstream API errors.
//
// Parameters:
//   - body: Original request body JSON
//   - provider: Provider name (determines which fields to strip)
//
// Returns:
//   - Modified request body JSON with thinking configuration removed
//   - Original body is returned unchanged if:
//   - body is empty or invalid JSON
//   - provider is unknown
//   - no thinking configuration found
func StripThinkingConfig(body []byte, provider string) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}

	var paths []string
	switch provider {
	case "claude":
		// Native: thinking + output_config.effort. Also strip alien OpenAI/Codex/Gemini fields
		// to avoid {"detail":"Unsupported parameter: reasoning_effort"} on cross-provider passthrough.
		paths = []string{"thinking", "output_config.effort", "reasoning_effort", "reasoning", "reasoning.effort", "generationConfig.thinkingConfig", "request.generationConfig.thinkingConfig"}
	case "gemini":
		paths = []string{"generationConfig.thinkingConfig", "reasoning_effort", "reasoning", "reasoning.effort", "thinking", "output_config.effort", "request.generationConfig.thinkingConfig"}
	case "antigravity":
		paths = []string{"request.generationConfig.thinkingConfig", "reasoning_effort", "reasoning", "reasoning.effort", "thinking", "output_config.effort", "generationConfig.thinkingConfig"}
	case "interactions":
		paths = []string{
			"generation_config.thinking_level",
			"generation_config.thinkingLevel",
			"generation_config.thinking_budget",
			"generation_config.thinkingBudget",
			"generation_config.thinking_summaries",
			"generation_config.thinkingSummaries",
			"generation_config.thinking_config",
			"generation_config.thinkingConfig",
			"reasoning_effort",
			"reasoning",
			"reasoning.effort",
			"thinking",
		}
	case "openai":
		paths = []string{"reasoning_effort", "reasoning"}
	case "kimi", "kimi-ai", "kimi.ai", "kimi.com":
		paths = []string{
			"reasoning_effort",
			"thinking",
			"reasoning",
			"reasoning.effort",
			"output_config.effort",
			"generationConfig.thinkingConfig",
			"request.generationConfig.thinkingConfig",
		}
	case "codex", "xai", "openai-response", "openai-responses", "responses":
		// Codex native is reasoning.effort (object). Strip flat reasoning_effort alias that
		// upstream rejects with {"detail":"Unsupported parameter: reasoning_effort"}.
		paths = []string{"reasoning", "reasoning.effort", "reasoning_effort", "thinking", "output_config.effort", "generationConfig.thinkingConfig", "request.generationConfig.thinkingConfig"}
	default:
		return body
	}

	result := body
	for _, path := range paths {
		result, _ = sjson.DeleteBytes(result, path)
	}

	// If reasoning.effort was deleted and reasoning object is now empty, clean it up.
	if r := gjson.GetBytes(result, "reasoning"); r.Exists() && r.IsObject() && len(r.Map()) == 0 {
		result, _ = sjson.DeleteBytes(result, "reasoning")
	}

	// Avoid leaving an empty output_config object for Claude when effort was the only field.
	if provider == "claude" {
		if oc := gjson.GetBytes(result, "output_config"); oc.Exists() && oc.IsObject() && len(oc.Map()) == 0 {
			result, _ = sjson.DeleteBytes(result, "output_config")
		}
	}
	return result
}
