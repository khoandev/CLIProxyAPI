package modelversion

import (
	"regexp"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
)

const maxFallbackChainSteps = 16

var dateRevisionSuffix = regexp.MustCompile(`-20\d{6}$`)

var (
	claudeMinorFamily = regexp.MustCompile(`^claude-(opus|sonnet)-(\d+)(?:-(\d+))?$`)
	glm5Family        = regexp.MustCompile(`^glm-5(?:\.(\d+))?$`)
	kimiK2Family      = regexp.MustCompile(`^kimi-k2\.(\d+)(?:-code(?:-highspeed)?)?$`)
	gpt5Family        = regexp.MustCompile(`^gpt-5(?:\.(\d+))?(?:-mini)?$`)
	minimaxMFamily    = regexp.MustCompile(`^minimax-m(\d+)(?:\.(\d+))?$`)
	// grok-4.5 / grok-4.3 / grok-4.20-* (not grok-build-*, imagine, etc.)
	grokFamily = regexp.MustCompile(`^grok-(\d+)\.(\d+)(?:-[\w.-]+)?$`)
)

// gpt56CodenameFallback maps GPT-5.6 codenames to their first registered
// downgrade target. After this hop, normal gpt-5 family rank logic applies.
var gpt56CodenameFallback = map[string]string{
	"gpt-5.6-terra": "gpt-5.4",
	"gpt-5.6-sol":   "gpt-5.5",
	"gpt-5.6-luna":  "gpt-5.4",
}

type familyRank struct {
	key  string
	rank int64
}

// Next returns the next lower registered model in the same family, or "" if none.
// providers limits candidates to models served by at least one of these providers (empty = any registered).
func Next(requested string, providers []string) string {
	return nextWithCandidates(requested, registeredCandidates(providers))
}

// nextWithCandidates is the hot-path ranking logic. It performs no registry
// access — candidates must be a pre-snapshotted list of registered model IDs.
func nextWithCandidates(requested string, candidates []string) string {
	parsed := thinking.ParseSuffix(requested)
	base := stripTrailingDateRevision(strings.ToLower(strings.TrimSpace(parsed.ModelName)))
	if base == "" {
		return ""
	}

	if mapped, ok := gpt56CodenameFallback[base]; ok {
		for _, id := range candidates {
			candBase := stripTrailingDateRevision(strings.ToLower(strings.TrimSpace(id)))
			if candBase == mapped {
				return withThinkingSuffix(id, parsed)
			}
		}
		// Mapped target not registered — continue as if requested mapped base.
		base = mapped
	}

	baseFamily, ok := familyRankForBase(base)
	if !ok {
		return ""
	}
	var bestID string
	var bestRank int64 = -1
	for _, id := range candidates {
		candBase := stripTrailingDateRevision(strings.ToLower(strings.TrimSpace(id)))
		candFamily, okCand := familyRankForBase(candBase)
		if !okCand || candFamily.key != baseFamily.key {
			continue
		}
		if candFamily.rank >= baseFamily.rank {
			continue
		}
		if candFamily.rank > bestRank {
			bestRank = candFamily.rank
			bestID = id
		}
	}
	if bestID == "" {
		return ""
	}
	return withThinkingSuffix(bestID, parsed)
}

// Chain returns ordered downgrade steps after requested (each passes registry availability).
func Chain(requested string, providers []string) []string {
	return chainWithCandidates(requested, registeredCandidates(providers))
}

// chainWithCandidates walks nextWithCandidates repeatedly against a single
// pre-snapshotted candidate list, so a multi-hop fallback sequence performs
// exactly one registry scan instead of one per hop.
func chainWithCandidates(requested string, candidates []string) []string {
	current := requested
	out := make([]string, 0, 4)
	for range maxFallbackChainSteps {
		next := nextWithCandidates(current, candidates)
		if next == "" {
			break
		}
		out = append(out, next)
		current = next
	}
	return out
}

func stripTrailingDateRevision(base string) string {
	return dateRevisionSuffix.ReplaceAllString(base, "")
}

func withThinkingSuffix(model string, parsed thinking.SuffixResult) string {
	if parsed.HasSuffix {
		return model + "(" + parsed.RawSuffix + ")"
	}
	return model
}

func registeredCandidates(providers []string) []string {
	reg := registry.GetGlobalRegistry()
	ids := reg.RegisteredModelIDs()
	if len(providers) == 0 {
		filtered := make([]string, 0, len(ids))
		for _, id := range ids {
			if reg.GetModelCount(id) > 0 {
				filtered = append(filtered, id)
			}
		}
		return filtered
	}
	want := normalizeProviders(providers)
	filtered := make([]string, 0, len(ids))
	for _, id := range ids {
		if reg.GetModelCount(id) <= 0 {
			continue
		}
		modelProviders := reg.GetModelProviders(id)
		if providerIntersects(modelProviders, want) {
			filtered = append(filtered, id)
		}
	}
	return filtered
}

func normalizeProviders(providers []string) map[string]struct{} {
	out := make(map[string]struct{}, len(providers))
	for _, p := range providers {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	return out
}

func providerIntersects(modelProviders []string, want map[string]struct{}) bool {
	for _, p := range modelProviders {
		p = strings.ToLower(strings.TrimSpace(p))
		if _, ok := want[p]; ok {
			return true
		}
	}
	return false
}

func familyRankForBase(base string) (familyRank, bool) {
	if m := claudeMinorFamily.FindStringSubmatch(base); len(m) >= 3 {
		major := parseInt64(m[2])
		minor := int64(0)
		if len(m) >= 4 && m[3] != "" {
			minor = parseInt64(m[3])
		}
		return familyRank{
			key:  "claude-" + m[1],
			rank: major*100 + minor,
		}, true
	}
	if m := glm5Family.FindStringSubmatch(base); len(m) >= 1 {
		patch := int64(0)
		if len(m) >= 2 && m[1] != "" {
			patch = parseInt64(m[1])
		}
		return familyRank{key: "glm-5", rank: 500 + patch*10}, true
	}
	if m := kimiK2Family.FindStringSubmatch(base); len(m) == 2 {
		gen := parseInt64(m[1])
		return familyRank{key: "kimi-k2", rank: gen * 10}, true
	}
	if m := gpt5Family.FindStringSubmatch(base); len(m) >= 1 {
		minor := false
		if strings.HasSuffix(base, "-mini") {
			minor = true
		}
		patch := int64(0)
		if len(m) >= 2 && m[1] != "" {
			patch = parseInt64(m[1])
		}
		rank := int64(500) + patch*10
		if minor {
			rank--
		}
		return familyRank{key: "gpt-5", rank: rank}, true
	}
	if m := minimaxMFamily.FindStringSubmatch(base); len(m) >= 2 {
		major := parseInt64(m[1])
		minor := int64(0)
		if len(m) >= 3 && m[2] != "" {
			minor = parseInt64(m[2])
		}
		return familyRank{key: "minimax-m", rank: major*1000 + minor}, true
	}
	if m := grokFamily.FindStringSubmatch(base); len(m) >= 3 {
		major := parseInt64(m[1])
		minor := parseInt64(m[2])
		// Product order: 4.5 > 4.3 > 4.20-* (not pure decimal — 4.20 is older line).
		rank := major*100 + minor*10
		if minor >= 10 {
			rank = major*100 + minor
		}
		return familyRank{key: "grok-" + m[1], rank: rank}, true
	}
	return familyRank{}, false
}

func parseInt64(s string) int64 {
	var n int64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int64(ch-'0')
	}
	return n
}
