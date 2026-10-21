package helps

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestStopChunkTrackerBoundsEntries proves the tracker never holds more
// entries than its bound.
//
// The tracker is fed by the client stream, so the number of entries is client
// controlled.
func TestStopChunkTrackerBoundsEntries(t *testing.T) {
	tracker := newStopChunkTracker(64)
	for i := range 512 {
		tracker.remember(fmt.Sprintf("trace-%d", i))
	}
	if got := tracker.size(); got > 64 {
		t.Fatalf("tracker entries = %d, want at most 64", got)
	}
}

// TestStopChunkTrackerExpiresEntries proves an expired trace ID is reported as
// absent and dropped.
func TestStopChunkTrackerExpiresEntries(t *testing.T) {
	tracker := newStopChunkTracker(64)
	now := time.Unix(1_800_000_000, 0)
	tracker.nowFunc = func() time.Time { return now }

	tracker.remember("trace-1")
	if !tracker.has("trace-1") {
		t.Fatal("remembered trace ID was reported as absent")
	}

	now = now.Add(stopChunkTTL + time.Second)
	if tracker.has("trace-1") {
		t.Fatal("expired trace ID was reported as present")
	}
	if got := tracker.size(); got != 0 {
		t.Fatalf("tracker entries after expiry = %d, want 0", got)
	}
}

// TestStopChunkTrackerForget proves a forgotten trace ID is reported as
// absent.
func TestStopChunkTrackerForget(t *testing.T) {
	tracker := newStopChunkTracker(64)
	tracker.remember("trace-1")
	tracker.forget("trace-1")
	if tracker.has("trace-1") {
		t.Fatal("forgotten trace ID was reported as present")
	}
}

// TestFilterSSEUsageMetadataKeepsTailUsageForRememberedTrace proves the
// tracker keeps the existing behaviour: the usage chunk that follows a stop
// chunk without usage metadata keeps its usage metadata, because the stop
// chunk already ended the response.
func TestFilterSSEUsageMetadataKeepsTailUsageForRememberedTrace(t *testing.T) {
	const traceID = "tracker-tail-usage"
	stopChunk := `{"traceId":"` + traceID + `","candidates":[{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}`
	usageChunk := `{"traceId":"` + traceID + `","usageMetadata":{"promptTokenCount":10,"totalTokenCount":15}}`

	payload := []byte("data: " + stopChunk + "\ndata: " + usageChunk + "\n")
	filtered := string(FilterSSEUsageMetadata(payload))
	if !strings.Contains(filtered, "usageMetadata") {
		t.Fatalf("tail usage metadata was stripped: %s", filtered)
	}

	// A usage chunk of an unknown trace keeps being stripped.
	otherPayload := []byte(`data: {"traceId":"tracker-other","usageMetadata":{"promptTokenCount":10,"totalTokenCount":15}}` + "\n")
	other := string(FilterSSEUsageMetadata(otherPayload))
	if strings.Contains(other, "usageMetadata") {
		t.Fatalf("usage metadata of an unknown trace survived: %s", other)
	}
}
