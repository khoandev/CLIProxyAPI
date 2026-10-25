package common

import (
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ClaudeMessageAccumulator groups consecutive Claude messages by role.
type ClaudeMessageAccumulator struct {
	messages     [][]byte
	role         string
	content      [][]byte
	toolUseParts [][]byte
}

// NewClaudeMessageAccumulator creates an accumulator sized for the expected messages.
func NewClaudeMessageAccumulator(capacity int) *ClaudeMessageAccumulator {
	return &ClaudeMessageAccumulator{
		messages: NewRawArrayItems(int64(capacity)),
	}
}

// Append adds one Claude-shaped message to the current role turn.
func (a *ClaudeMessageAccumulator) Append(message []byte) {
	if len(message) == 0 {
		return
	}
	root := util.ParseGJSONBytesNoCopy(message)
	role := root.Get("role").String()
	if role != "user" && role != "assistant" {
		return
	}
	parts := claudeMessageContentParts(root.Get("content"))
	if len(parts) == 0 {
		return
	}
	if a.role != "" && a.role != role {
		a.Flush()
	}
	a.role = role
	for _, part := range parts {
		if role == "assistant" && gjson.GetBytes(part, "type").String() == "tool_use" {
			a.toolUseParts = append(a.toolUseParts, part)
			continue
		}
		a.content = append(a.content, part)
	}
}

// Flush closes the current role turn while keeping accumulated messages.
func (a *ClaudeMessageAccumulator) Flush() {
	if a.role == "" {
		return
	}
	parts := a.content
	if len(a.toolUseParts) > 0 {
		combined := make([][]byte, 0, len(a.content)+len(a.toolUseParts))
		combined = append(combined, a.content...)
		combined = append(combined, a.toolUseParts...)
		parts = combined
	}
	if len(parts) > 0 {
		message := []byte(`{"role":"","content":[]}`)
		message, _ = sjson.SetBytes(message, "role", a.role)
		// The items are spliced into the empty array, so the content is copied
		// once instead of being joined and then written into the message.
		message = SetRawArrayItems(message, "content", parts)
		a.messages = append(a.messages, message)
	}
	a.role = ""
	a.content = nil
	a.toolUseParts = nil
}

// Messages flushes the final turn and returns all accumulated messages.
func (a *ClaudeMessageAccumulator) Messages() [][]byte {
	a.Flush()
	return a.messages
}

// AlignClaudeToolResults orders tool_result blocks by the preceding tool_use IDs,
// preserving non-result content blocks at their existing indexes. It returns nil
// when the content needs no reordering, so callers keep the original
// gjson.Result and pay no copy at all.
func AlignClaudeToolResults(content gjson.Result, toolUseIDs []string) []byte {
	if !content.IsArray() || len(toolUseIDs) == 0 {
		return nil
	}

	parts := content.Array()
	toolResultIndices := make([]int, 0, len(toolUseIDs))
	for i, part := range parts {
		if part.Get("type").String() == "tool_result" {
			toolResultIndices = append(toolResultIndices, i)
		}
	}
	if len(toolResultIndices) != len(toolUseIDs) {
		return nil
	}

	// ordered maps each output slot to the index of the part it receives.
	ordered := make([]int, len(parts))
	for i := range ordered {
		ordered[i] = i
	}
	used := make([]bool, len(toolResultIndices))
	for i, toolUseID := range toolUseIDs {
		matched := -1
		for resultIndex, partIndex := range toolResultIndices {
			if !used[resultIndex] && toolUseID != "" && parts[partIndex].Get("tool_use_id").String() == toolUseID {
				matched = resultIndex
				break
			}
		}
		if matched < 0 {
			return nil
		}
		used[matched] = true
		ordered[toolResultIndices[i]] = toolResultIndices[matched]
	}

	size := len(parts) + 1
	for _, part := range parts {
		size += len(part.Raw)
	}
	out := make([]byte, 0, size)
	out = append(out, '[')
	for i, partIndex := range ordered {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, parts[partIndex].Raw...)
	}
	out = append(out, ']')
	return out
}

func claudeMessageContentParts(content gjson.Result) [][]byte {
	if !content.Exists() || content.Type == gjson.Null {
		return nil
	}
	if content.Type == gjson.String {
		if content.String() == "" {
			return nil
		}
		part := []byte(`{"type":"text","text":""}`)
		part, _ = sjson.SetBytes(part, "text", content.String())
		return [][]byte{part}
	}
	if !content.IsArray() {
		return nil
	}
	parts := make([][]byte, 0, len(content.Array()))
	content.ForEach(func(_, part gjson.Result) bool {
		if part.IsObject() {
			parts = append(parts, []byte(part.Raw))
		}
		return true
	})
	return parts
}
