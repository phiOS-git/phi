package query

import (
	"sort"
	"strings"
)

// matchWeight is a plain, documented heuristic — not a document-fixed
// formula, since none exists anywhere in this project's plan; S-33's own
// AGENT bullet explicitly calls ranking "the risky, iterative part" and
// asks for it to be validated in a terminal before any GUI work, which is
// exactly what rank_test.go below does with fixed cases rather than real
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
// before any per-query score is even looked at — docs/TODO.md's own
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
// Windows are not named in the backlog entry but sit just under apps
// anyway — query.go's own header already calls switching to an open
// window "likely the most frequent launcher action on a tiling
// compositor," which puts it beside launching, not beside a file search.
// Categories the backlog entry did not name (system actions, ssh hosts,
// zoxide, run-command) keep their previous relative order, placed below
// math since each is a narrower, more deliberate action a query rarely
// triggers by accident. Web search stays the fallback of last resort — it
// already refuses to answer unless nothing else looked promising.
const (
	tierApps      = 5000.0
	tierWindows   = 4000.0
	tierFiles     = 3000.0
	tierMath      = 2000.0
	tierAction    = 1000.0
	tierWebSearch = 0.0
)

var providerTiers = map[string]float64{
	"application": tierApps,
	"window":      tierWindows,
	"file":        tierFiles,
	"calculator":  tierMath,
	"currency":    tierMath,
	"system":      tierAction,
	"ssh":         tierAction,
	"directory":   tierAction,
	"command":     tierAction,
	"websearch":   tierWebSearch,
}

// providerTier defaults an unrecognised provider name to tierAction — the
// same "deliberate, narrower action" band as the closed set above — rather
// than to either extreme, so a future provider nobody updated this map for
// degrades to a reasonable middle instead of silently dominating or
// vanishing.
func providerTier(provider string) float64 {
	if t, ok := providerTiers[provider]; ok {
		return t
	}
	return tierAction
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
