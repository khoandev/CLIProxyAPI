package common

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func GeminiTokenCountJSON(count int64) []byte {
	out := make([]byte, 0, 96)
	out = append(out, `{"totalTokens":`...)
	out = strconv.AppendInt(out, count, 10)
	out = append(out, `,"promptTokensDetails":[{"modality":"TEXT","tokenCount":`...)
	out = strconv.AppendInt(out, count, 10)
	out = append(out, `}]}`...)
	return out
}

func ClaudeInputTokensJSON(count int64) []byte {
	out := make([]byte, 0, 32)
	out = append(out, `{"input_tokens":`...)
	out = strconv.AppendInt(out, count, 10)
	out = append(out, '}')
	return out
}

// NewRawArrayItems creates a raw item slice sized for the expected input.
func NewRawArrayItems(capacity int64) [][]byte {
	if capacity <= 0 {
		return nil
	}
	return make([][]byte, 0, int(capacity))
}

// AppendRawJSONString merges one JSON value into an accumulated raw JSON string
// and returns the merged bytes, quotes included.
//
// JSON string escaping works per character, so concatenating the escaped bodies
// of two parts is identical to escaping their concatenation. Keeping the raw
// bytes avoids unescaping and re-escaping a multi-megabyte payload, and the
// first part is returned as is instead of being copied into a buffer.
func AppendRawJSONString(acc []byte, value gjson.Result) []byte {
	part := util.GJSONRawBytesNoCopy(value)
	if value.Type != gjson.String || len(part) < 2 || part[0] != '"' || part[len(part)-1] != '"' {
		// Non-string values, and any shape gjson did not hand back as a quoted
		// literal, are escaped here so the caller always gets valid raw JSON.
		escaped, err := json.Marshal(value.String())
		if err != nil {
			return acc
		}
		part = escaped
	}
	if len(acc) == 0 {
		return part
	}
	merged := make([]byte, 0, len(acc)+len(part)-2)
	merged = append(merged, acc[:len(acc)-1]...)
	merged = append(merged, part[1:]...)
	return merged
}

func JoinRawArray(items [][]byte) []byte {
	if len(items) == 0 {
		return []byte("[]")
	}
	size := len(items) + 1
	for _, item := range items {
		size += len(item)
	}
	out := make([]byte, 0, size)
	out = append(out, '[')
	for i, item := range items {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, item...)
	}
	return append(out, ']')
}

// SetRawArrayItems replaces an empty JSON array at path with raw items.
// The items are spliced in place so the document is copied once, no matter how
// many items there are. A document rewrite per item would copy the whole
// payload again for each item.
func SetRawArrayItems(data []byte, path string, items [][]byte) []byte {
	if len(items) == 0 {
		return data
	}
	array := gjson.GetBytes(data, path)
	if array.Raw == "[]" && array.Index >= 0 && array.Index+len(array.Raw) <= len(data) {
		size := len(items) + 1
		for _, item := range items {
			size += len(item)
		}
		out := make([]byte, 0, len(data)+size)
		out = append(out, data[:array.Index]...)
		out = append(out, '[')
		for i, item := range items {
			if i > 0 {
				out = append(out, ',')
			}
			out = append(out, item...)
		}
		out = append(out, ']')
		return append(out, data[array.Index+len(array.Raw):]...)
	}
	data, _ = sjson.SetRawBytes(data, path, JoinRawArray(items))
	return data
}

// SSEEventData builds one complete SSE frame. Each frame carries its own
// blank-line terminator so concatenated frames stay separable downstream.
func SSEEventData(event string, payload []byte) []byte {
	out := make([]byte, 0, len(event)+len(payload)+16)
	out = append(out, "event: "...)
	out = append(out, event...)
	out = append(out, '\n')
	out = append(out, "data: "...)
	out = append(out, payload...)
	return append(out, '\n', '\n')
}

func AppendSSEEventString(out []byte, event, payload string, trailingNewlines int) []byte {
	out = append(out, "event: "...)
	out = append(out, event...)
	out = append(out, '\n')
	out = append(out, "data: "...)
	out = append(out, payload...)
	for i := 0; i < trailingNewlines; i++ {
		out = append(out, '\n')
	}
	return out
}

func AppendSSEEventBytes(out []byte, event string, payload []byte, trailingNewlines int) []byte {
	out = append(out, "event: "...)
	out = append(out, event...)
	out = append(out, '\n')
	out = append(out, "data: "...)
	out = append(out, payload...)
	for i := 0; i < trailingNewlines; i++ {
		out = append(out, '\n')
	}
	return out
}

// SetStringWithoutHTMLEscape sets a string field in a JSON payload without escaping
// HTML characters (<, >, &). Standard sjson.Set/SetBytes delegates string values
// containing quotes or special characters to encoding/json.Marshal, which unconditionally
// escapes <, >, & to \u003c, \u003e, \u0026.
func SetStringWithoutHTMLEscape(data []byte, path, value string) ([]byte, error) {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if errEncode := enc.Encode(value); errEncode != nil {
		return sjson.SetBytes(data, path, value)
	}
	raw := bytes.TrimRight(buf.Bytes(), "\n")
	return sjson.SetRawBytes(data, path, raw)
}
