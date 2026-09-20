package query

import (
	"sort"
	"strings"
)

// matchWeight scores title matches: exact match (100), starts-with (80),
// word starts-with (60), substring (40), fuzzy (20), no match (0).
// Ties break by title length, then frecency.
const frecencyWeight = 15.0

// providerTier ranks result categories before any per-query score.
// Tiers (spaced 1000 apart): apps, windows, files, commands, phi,
// system/ssh/directory, ask-agent, websearch, math, currency.
// Category dominates: per-query contribution max (100+15) cannot spill
// into next tier.
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

// providerTier defaults unknown providers to tierOtherAction for robustness.
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

// isSubsequence reports if needle's runes appear in title in order ("fx" = "Firefox").
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

// Rank scores and sorts results. Provider-set Score is trusted as-is
// (plus frecency); otherwise matchWeight scores title, then providerTier.
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
