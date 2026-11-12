package auth

import (
	"fmt"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

// TestSchedulerModelShardsAreBoundedForUnknownModelNames verifies that the
// model shards of a provider stay bounded when a client keeps asking for model
// names that no auth supports.
func TestSchedulerModelShardsAreBoundedForUnknownModelNames(t *testing.T) {
	const authID = "shard-bound-auth"
	registerSchedulerModels(t, "gemini", "gemini-known-model", authID)

	scheduler := newSchedulerForTest(
		&RoundRobinSelector{},
		&Auth{ID: authID, Provider: "gemini", Attributes: map[string]string{"priority": "1"}},
	)

	scheduler.mu.Lock()
	providerState := scheduler.providers["gemini"]
	scheduler.mu.Unlock()
	if providerState == nil {
		t.Fatal("provider scheduler for gemini is missing")
	}

	now := time.Now()
	// The shard of a supported model holds the auth entry, so it must survive
	// the eviction of the shards the client creates with unknown model names.
	scheduler.mu.Lock()
	providerState.ensureModelLocked("gemini-known-model", now)
	scheduler.mu.Unlock()

	const extraModels = 500
	for index := range maxModelShardsPerProvider + extraModels {
		model := fmt.Sprintf("unknown-model-%d", index)
		scheduler.mu.Lock()
		providerState.ensureModelLocked(model, now)
		scheduler.mu.Unlock()
	}

	scheduler.mu.Lock()
	shardCount := len(providerState.modelShards)
	_, knownPresent := providerState.modelShards["gemini-known-model"]
	scheduler.mu.Unlock()

	t.Logf("model shards = %d, cap = %d", shardCount, maxModelShardsPerProvider)
	if shardCount > maxModelShardsPerProvider {
		t.Fatalf("model shards = %d, want at most %d", shardCount, maxModelShardsPerProvider)
	}
	if !knownPresent {
		t.Fatal("the shard of a supported model must not be evicted")
	}
}

// TestSchedulerModelShardsPruneOnlyIdleEmptyShards verifies that the idle
// sweep drops the shards without auth entries and keeps the shards that still
// hold scheduling state.
func TestSchedulerModelShardsPruneOnlyIdleEmptyShards(t *testing.T) {
	const authID = "shard-idle-auth"
	registerSchedulerModels(t, "gemini", "gemini-known-model", authID)

	scheduler := newSchedulerForTest(
		&RoundRobinSelector{},
		&Auth{ID: authID, Provider: "gemini", Attributes: map[string]string{"priority": "1"}},
	)

	scheduler.mu.Lock()
	providerState := scheduler.providers["gemini"]
	scheduler.mu.Unlock()
	if providerState == nil {
		t.Fatal("provider scheduler for gemini is missing")
	}

	now := time.Now()
	scheduler.mu.Lock()
	providerState.ensureModelLocked("unknown-model", now)
	providerState.ensureModelLocked("gemini-known-model", now)
	scheduler.mu.Unlock()

	// Inside the idle timeout the empty shard is kept.
	scheduler.mu.Lock()
	providerState.pruneModelShardsLocked(now.Add(modelShardIdleTTL - time.Minute))
	_, emptyKept := providerState.modelShards["unknown-model"]
	scheduler.mu.Unlock()
	if !emptyKept {
		t.Fatal("an empty shard must be kept until it is idle")
	}

	// After the idle timeout the empty shard is dropped, the shard that holds
	// the auth stays.
	scheduler.mu.Lock()
	providerState.pruneModelShardsLocked(now.Add(modelShardIdleTTL + time.Minute))
	_, emptyDropped := providerState.modelShards["unknown-model"]
	knownShard, knownPresent := providerState.modelShards["gemini-known-model"]
	scheduler.mu.Unlock()

	if emptyDropped {
		t.Fatal("an idle empty shard must be dropped")
	}
	if !knownPresent {
		t.Fatal("the shard of a supported model must not be dropped")
	}
	if knownShard == nil || len(knownShard.entries) != 1 {
		t.Fatalf("the shard of a supported model lost its entries: %+v", knownShard)
	}
	if knownShard.entries[authID] == nil {
		t.Fatal("the shard of a supported model lost its auth entry")
	}
}

// TestSchedulerUpsertAuthPrunesIdleEmptyShards verifies that the reconcile of
// the credential upsert drops the model shards that are idle and hold no auth.
func TestSchedulerUpsertAuthPrunesIdleEmptyShards(t *testing.T) {
	const authID = "shard-reconcile-auth"
	registerSchedulerModels(t, "gemini", "gemini-known-model", authID)

	scheduler := newSchedulerForTest(
		&RoundRobinSelector{},
		&Auth{ID: authID, Provider: "gemini", Attributes: map[string]string{"priority": "1"}},
	)

	base := time.Now()
	scheduler.mu.Lock()
	providerState := scheduler.providers["gemini"]
	if providerState == nil {
		scheduler.mu.Unlock()
		t.Fatal("provider scheduler for gemini is missing")
	}
	providerState.ensureModelLocked("unknown-model", base.Add(-2*modelShardIdleTTL))
	_, staleExists := providerState.modelShards["unknown-model"]
	scheduler.mu.Unlock()
	if !staleExists {
		t.Fatal("the stale shard must exist before the reconcile")
	}

	previousNow := schedulerNowFunc
	schedulerNowFunc = func() time.Time { return base }
	t.Cleanup(func() { schedulerNowFunc = previousNow })

	// The upsert must not be rejected as stale, so the auth carries the epoch
	// the registry knows for it.
	scheduler.upsertAuth(&Auth{
		ID:                authID,
		Provider:          "gemini",
		RegistrationEpoch: registry.GetGlobalRegistry().ClientRegistrationEpoch(authID),
		Attributes:        map[string]string{"priority": "1"},
	})

	scheduler.mu.Lock()
	_, stalePresent := providerState.modelShards["unknown-model"]
	scheduler.mu.Unlock()
	if stalePresent {
		t.Fatal("the reconcile of the auth upsert must drop the idle empty shard")
	}
}

// TestSchedulerModelShardsKeepCooldownState verifies that a shard whose auth
// waits for a cooldown is not dropped, because dropping it would lose the
// cooldown deadline.
func TestSchedulerModelShardsKeepCooldownState(t *testing.T) {
	const authID = "shard-cooldown-auth"
	registerSchedulerModels(t, "gemini", "gemini-known-model", authID)

	scheduler := newSchedulerForTest(
		&RoundRobinSelector{},
		&Auth{ID: authID, Provider: "gemini", Attributes: map[string]string{"priority": "1"}},
	)

	now := time.Now()
	scheduler.mu.Lock()
	providerState := scheduler.providers["gemini"]
	if providerState == nil {
		scheduler.mu.Unlock()
		t.Fatal("provider scheduler for gemini is missing")
	}
	shard := providerState.ensureModelLocked("gemini-known-model", now)
	entry := shard.entries[authID]
	if entry == nil {
		scheduler.mu.Unlock()
		t.Fatal("the shard of a supported model has no auth entry")
	}
	entry.state = scheduledStateBlocked
	entry.nextRetryAt = now.Add(time.Hour)
	providerState.pruneModelShardsLocked(now.Add(10 * modelShardIdleTTL))
	keptShard, keptPresent := providerState.modelShards["gemini-known-model"]
	scheduler.mu.Unlock()

	if !keptPresent {
		t.Fatal("a shard that holds a cooldown must not be dropped")
	}
	if keptShard == nil || keptShard.entries[authID] == nil {
		t.Fatal("a shard that holds a cooldown must keep its auth entry")
	}
	if keptShard.entries[authID].nextRetryAt.IsZero() {
		t.Fatal("a shard that holds a cooldown must keep the cooldown deadline")
	}
}
