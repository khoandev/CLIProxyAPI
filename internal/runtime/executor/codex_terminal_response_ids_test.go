package executor

import (
	"strconv"
	"testing"
)

func TestTerminalResponseIDRingKeepsRecentIDs(t *testing.T) {
	var ring terminalResponseIDRing
	for i := 0; i < codexTerminalResponseIDLimit*2; i++ {
		ring.add("resp-" + strconv.Itoa(i))
	}

	if !ring.has("resp-" + strconv.Itoa(codexTerminalResponseIDLimit*2-1)) {
		t.Fatalf("the newest id must be remembered")
	}
	if ring.has("resp-0") {
		t.Fatalf("the oldest id must be forgotten once the ring is full")
	}
	if ring.count != codexTerminalResponseIDLimit {
		t.Fatalf("count = %d, want %d", ring.count, codexTerminalResponseIDLimit)
	}
}

func TestTerminalResponseIDRingDeduplicates(t *testing.T) {
	var ring terminalResponseIDRing
	ring.add("resp-1")
	ring.add("resp-1")
	if ring.count != 1 {
		t.Fatalf("count = %d, want 1 after a duplicate add", ring.count)
	}
	if !ring.has("resp-1") {
		t.Fatalf("duplicate add must keep the id")
	}
}

func TestTerminalResponseIDRingReset(t *testing.T) {
	var ring terminalResponseIDRing
	ring.add("resp-1")
	ring.add("resp-2")
	ring.reset()
	if ring.has("resp-1") || ring.has("resp-2") {
		t.Fatalf("reset must forget every id")
	}
	if ring.count != 0 || ring.next != 0 {
		t.Fatalf("reset left count=%d next=%d", ring.count, ring.next)
	}
	// The ring must be usable again after a reset.
	ring.add("resp-3")
	if !ring.has("resp-3") {
		t.Fatalf("ring must accept ids after a reset")
	}
}

func TestTerminalResponseIDRingEmptyValueMatchesOldMapSemantics(t *testing.T) {
	var ring terminalResponseIDRing
	if ring.has("") {
		t.Fatalf("an empty ring must not report an empty id as terminal")
	}
	ring.add("")
	if !ring.has("") {
		t.Fatalf("an explicitly added empty id must be remembered, as the old map did")
	}
}
