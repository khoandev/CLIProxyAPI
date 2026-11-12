package cache

import (
	"sort"
	"time"
)

// TrimExpiredEntries removes the entries of a TTL map that expired and, when
// the map is still over the limit, the entries that expire first.
//
// The caller owns the lock of the map. The limit is a hard bound on the number
// of entries, so a map that is filled with client controlled keys cannot grow
// without limit. Entries that expire first are dropped first: they are the
// ones the next request is most likely to replace anyway.
//
// The helper returns the number of removed entries.
func TrimExpiredEntries[K comparable, V any](entries map[K]V, limit int, expireAt func(V) time.Time, now time.Time) int {
	if len(entries) == 0 {
		return 0
	}
	removed := 0
	for key, entry := range entries {
		if !expireAt(entry).After(now) {
			delete(entries, key)
			removed++
		}
	}
	if limit <= 0 || len(entries) <= limit {
		return removed
	}

	type agedEntry struct {
		key    K
		expire time.Time
	}
	aged := make([]agedEntry, 0, len(entries))
	for key, entry := range entries {
		aged = append(aged, agedEntry{key: key, expire: expireAt(entry)})
	}
	sort.Slice(aged, func(i, j int) bool {
		if aged[i].expire.Equal(aged[j].expire) {
			return false
		}
		return aged[i].expire.Before(aged[j].expire)
	})
	for index := 0; index < len(aged) && len(entries) > limit; index++ {
		delete(entries, aged[index].key)
		removed++
	}
	return removed
}
