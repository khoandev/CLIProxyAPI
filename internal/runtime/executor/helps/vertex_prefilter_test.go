package helps

import (
	"strings"
	"testing"
)

// TestJSONMayContainASCIISearchesNeedlePrefix proves a long needle is matched
// by its leading bytes. The search for a long needle is much slower, and a
// body that holds the whole needle always holds that prefix, so the answer
// only becomes more cautious and never skips a walk it should have performed.
func TestJSONMayContainASCIISearchesNeedlePrefix(t *testing.T) {
	body := []byte(`{"system":"naming a coding session"}`)
	if !jsonMayContainASCII(body, "naming a coding session") {
		t.Fatal("a body holding the whole needle must match")
	}
	prefixOnly := []byte(`{"system":"naming a"}`)
	if !jsonMayContainASCII(prefixOnly, "naming a coding session") {
		t.Fatal("a body holding the needle prefix must match")
	}
	if jsonMayContainASCII([]byte(`{"system":"hello"}`), "naming a coding session") {
		t.Fatal("a body holding neither the needle nor its prefix must not match")
	}
}

// TestStripVertexToolCallIDsSkipsUnrelatedPayload proves the pre-filter keeps
// a payload that mentions neither key untouched, and still rewrites a payload
// that does.
func TestStripVertexToolCallIDsSkipsUnrelatedPayload(t *testing.T) {
	unrelated := []byte(`{"contents":[{"role":"user","parts":[{"text":"` + strings.Repeat("x", 1<<16) + `"}]}]}`)
	output := StripVertexOpenAIResponsesToolCallIDs(unrelated, "openai-response")
	if &output[0] != &unrelated[0] {
		t.Fatal("an unrelated payload was copied")
	}

	withID := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup"}}]}]}`)
	rewritten := StripVertexOpenAIResponsesToolCallIDs(withID, "openai-response")
	if strings.Contains(string(rewritten), `"call_1"`) {
		t.Fatalf("function call id was not removed: %s", rewritten)
	}
}
