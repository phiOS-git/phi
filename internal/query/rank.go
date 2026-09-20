package query

import (
	"sort"
	"strings"
)

// matchWeight is a plain, documented heuristic — not a formula set in stone.
// Ranking is validated via fixed test cases in rank_test.go rather than real
// usage this agent cannot generate. Tiers, highest first:
//
//	100  Title is exactly the query (case-insensitive)
//	 80  Title starts with the query
//	 60  Some word in the title starts with the query (e.g. "fi" -> "Firefox")
//	 40  Title contains the query as a substring
//	 20  Query's characters appear in the title in order (a loose fuzzy match)
//	  0  No match at all — excluded from the result set entirely
//
// A shorter title wins a tie at the same tier (a more specific match), and
// frecency then adds up to frecencyWeight on top, so a query that matches
// two titles equally well is decided by what was actually used before, not
// by insertion order.
const frecencyWeight = 15.0

// providerTier ranks a whole category of result above or below another,
// before any per-query score is even looked at — own
// complaint: "it has latest features appearing first (like the calculator)
// but it does not make sense. Apps should be always first, non hidden
// files second, math when obvious." Without this, CalculatorProvider's
// flat Score of 100 (its own top confidence — matchWeight cannot judge a
// numeric answer against the query that produced it, see
// TestRankTrustsExplicitProviderScore) ties or beats a genuine exact-title
// app match, which is the exact bug reported.
//
// Tiers are spaced 1000 apart: the largest possible per-query contribution
// (matchWeight's 100 plus frecencyWeight's 15) cannot spill into the next
// tier, so category always wins over match quality, and match quality
// (plus frecency) still decides the order within a category.
//
// The order below is full category list, verbatim:
// apps, HOME files, commands, phi commands, search any file, ask ai agent,
// search web, math, conversion — a deliberate flip from the previous
// scheme, where math/currency outranked commands and web search. Two
// categories the list names — "search any file" (broader than the
// existing home-directory-only FilesProvider) and, at the time this tier
// table was written, "ask ai agent" — had no provider yet; ask-ai-agent
// has one now (AskAgentProvider, askagent.go) and keeps the list's tier;
// "search any file" still doesn't exist, so its tier is intentionally not
// reserved here — add it when that provider is built, immediately below
// tierPhi.
//
// Windows, system actions, ssh hosts and zoxide directory jumps are not
// named in the list. Windows sit just under apps, unchanged from before —
// query.go's own header already calls switching to an open window "likely
// the most frequent launcher action on a tiling compositor," which puts it
// beside launching, not beside a file search. System/ssh/directory keep
// their previous grouping with commands and phi verbs (all four are a
// single deliberate, narrow action triggered by fairly exact query syntax)
// rather than being stranded below math, which the previous comment's
// "keep below math" convention would now put them at the very bottom of
// the whole list — a much bigger demotion than the backlog entry asked
// for.
const (
	tierApps        = 9000.0
	tierWindows     = 8000.0
	tierFiles       = 7000.0
	tierCommand     = 6000.0
	tierPhi         = 5000.0
	tierOtherAction = 4000.0 // system actions, ssh hosts, zoxide — see comment above
	tierAskAgent    = 3000.0
	tierWebSearch   = 2000.0
	tierMath        = 1000.0
	tierCurrency    = 0.0
)

var providerTiers = map[string]float64{
	"application": tierApps,
	"window":      tierWindows,
	"file":        tierFiles,
	"command":     tierCommand,
	"phi":         tierPhi,
	"system":      tierOtherAction,
	"ssh":         tierOtherAction,
	"directory":   tierOtherAction,
	"agent":       tierAskAgent,
	"websearch":   tierWebSearch,
	"sitesearch":  tierWebSearch, // wiki/yt/arch/rddt — same band as the generic web search it sits beside
	"calculator":  tierMath,
	"currency":    tierCurrency,
}

// providerTier defaults an unrecognised provider name to tierOtherAction —
// the same "deliberate, narrower action" band used for the other unnamed
// categories — rather than to either extreme, so a future provider nobody
// updated this map for degrades to a reasonable middle instead of silently
// dominating or vanishing.
func providerTier(provider string) float64 {
	if t, ok := providerTiers[provider]; ok {
		return t
	}
	return tierOtherAction
}

func matchWeight(title, q string) float64 {
	if q == "" {
		return 0
	}
	t := strings.ToLower(title)
	needle := strings.ToLower(q)

	switch {
	case t == needle:
		return 100
	case strings.HasPrefix(t, needle):
		return 80
	case hasWordPrefix(t, needle):
		return 60
	case strings.Contains(t, needle):
		return 40
	case isSubsequence(t, needle):
		return 20
	default:
		return 0
	}
}

func hasWordPrefix(title, needle string) bool {
	for _, word := range strings.FieldsFunc(title, func(r rune) bool {
		return r == ' ' || r == '-' || r == '_' || r == '.'
	}) {
		if strings.HasPrefix(word, needle) {
			return true
		}
	}
	return false
}

// isSubsequence reports whether every rune of needle appears in title, in
// order, not necessarily contiguous — "fx" matches "Firefox".
func isSubsequence(title, needle string) bool {
	i := 0
	needleRunes := []rune(needle)
	if len(needleRunes) == 0 {
		return false
	}
	for _, r := range title {
		if i < len(needleRunes) && r == needleRunes[i] {
			i++
		}
	}
	return i == len(needleRunes)
}

// Rank scores and sorts results for query q. A Result with Score already
// set to a positive value by its own provider (the calculator's single
// answer, a system action matched by its own name) is trusted as-is and
// only gets the frecency term added — matchWeight is for providers that
// hand Rank a raw title to score against the query, which is most of them.
// providerTier is then added on top of either, so the category always
// dominates the ordering (see providerTier's own comment).
func Rank(results []Result, q string, frecency *Frecency) []Result {
	scored := make([]Result, 0, len(results))
	for _, r := range results {
		score := r.Score
		if score == 0 {
			score = matchWeight(r.Title, q)
			if score == 0 && q != "" {
				continue // no match at all — excluded, not ranked last
			}
		}
		if frecency != nil {
			score += frecency.Score(r.ID) * frecencyWeight
		}
		score += providerTier(r.Provider)
		r.Score = score
		scored = append(scored, r)
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return len(scored[i].Title) < len(scored[j].Title)
	})

	return scored
}
