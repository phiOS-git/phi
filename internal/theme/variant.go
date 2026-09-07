package theme

import "phi/internal/state"

// DefaultVariant is what an unset "theme.variant" state key means (master
// plan §5.6, bin/lib/tokens.sh's phios_variant). Falling back to it here
// rather than in internal/state is theme policy, not state policy: state.Get
// reports "unset" plainly, exactly as it does for any other key.
const DefaultVariant = "dark"

// CurrentVariant reads the active theme variant that a previous `phi theme
// set` recorded, falling back to DefaultVariant exactly as bin/lib/tokens.sh
// does — an unset key, or a state directory that cannot be read, is not an
// error.
func CurrentVariant() string {
	v, ok, err := state.Get("theme.variant")
	if err != nil || !ok || v == "" {
		return DefaultVariant
	}
	return v
}

// RecordVariant writes the active variant so the next CurrentVariant call —
// by phi itself, by `phi state get theme.variant`, and by bin/lib/tokens.sh's
// phios_variant, which reads the same file under a different name — picks it
// up. Set is what calls this; render and preview never do, since they only
// read a variant, they do not choose one.
func RecordVariant(variant string) error {
	return state.Set("theme.variant", variant)
}
