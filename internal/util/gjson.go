package util

import (
	"strings"
	"unsafe"

	"github.com/tidwall/gjson"
)

// GetGJSONBytesNoCopy returns a GJSON result that may reference data directly.
// Callers must not retain the result or mutate data while using it.
func GetGJSONBytesNoCopy(data []byte, path string) gjson.Result {
	if len(data) == 0 {
		return gjson.Result{}
	}
	return gjson.Get(unsafe.String(unsafe.SliceData(data), len(data)), path)
}

// ParseGJSONBytesNoCopy parses data into a GJSON result that references data
// directly. gjson.ParseBytes copies the whole document, which is prohibitive
// for multi-megabyte payloads. Callers must not retain the result or mutate
// data while using it.
func ParseGJSONBytesNoCopy(data []byte) gjson.Result {
	if len(data) == 0 {
		return gjson.Result{}
	}
	return gjson.Parse(unsafe.String(unsafe.SliceData(data), len(data)))
}

// JSONIndex holds the top-level members of a JSON object so that many path
// lookups do not rescan the whole document.
//
// A plain gjson lookup of an absent path scans the entire document, so a
// payload with many candidate paths costs one full scan per path. The index
// performs that scan once and then answers every lookup from the top-level
// members.
//
// The index aliases the source buffer, so the caller must not mutate the
// buffer while the index is in use.
type JSONIndex struct {
	root       gjson.Result
	fields     map[string]gjson.Result
	duplicates map[string]bool
	object     bool
}

// NewJSONIndex builds an index over the top-level members of body.
// Bodies that are not JSON objects fall back to plain gjson lookups.
func NewJSONIndex(body []byte) JSONIndex {
	index := JSONIndex{}
	if len(body) == 0 {
		return index
	}
	index.root = ParseGJSONBytesNoCopy(body)
	if !index.root.IsObject() {
		return index
	}
	index.object = true
	index.root.ForEach(func(key, value gjson.Result) bool {
		name := key.String()
		if _, exists := index.fields[name]; exists {
			if index.duplicates == nil {
				index.duplicates = make(map[string]bool)
			}
			index.duplicates[name] = true
			return true
		}
		if index.fields == nil {
			index.fields = make(map[string]gjson.Result, 8)
		}
		index.fields[name] = value
		return true
	})
	return index
}

// Get resolves path against the indexed object.
//
// Plain dotted paths resolve from the indexed top-level member, which matches
// gjson lookup semantics. Paths that use gjson modifiers, wildcards or array
// queries fall back to a full document lookup, and paths rooted at a
// duplicated member do the same so that repeated members keep gjson's
// first-match-wins behaviour.
func (index JSONIndex) Get(path string) gjson.Result {
	if index.root.Type != gjson.JSON {
		return gjson.Result{}
	}
	if path == "" {
		// gjson resolves an empty path to "not found".
		return gjson.Result{}
	}
	if !index.object || strings.ContainsAny(path, "@#|!*?\\") {
		return index.root.Get(path)
	}
	key, rest, nested := strings.Cut(path, ".")
	value, ok := index.fields[key]
	if !ok {
		return gjson.Result{}
	}
	if !nested {
		return value
	}
	if index.duplicates[key] {
		return index.root.Get(path)
	}
	resolved := value.Get(rest)
	if !resolved.Exists() {
		// A top-level gjson lookup that finds nothing reports an empty result,
		// so a nested miss must report the same shape.
		return gjson.Result{}
	}
	return resolved
}
