package helps

import "time"

// CodexCache carries the prompt cache identity of an upstream session.
//
// The Codex executors read the ID and write it into the upstream request as
// prompt_cache_key. The prompt cache store that used to sit next to this type
// had no caller and was removed, so the expiry field is currently unused.
type CodexCache struct {
	ID     string
	Expire time.Time
}
