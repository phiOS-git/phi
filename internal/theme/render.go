// Package theme replaces bin/phios-render and bin/lib/tokens.sh (S-12):
// rendering templates against design tokens, and, from design/adapters.txt,
// knowing where each themed target lives, how to reload it, and which class
// it belongs to.
package theme

import "regexp"

// variableRef matches a shell-style variable reference, $NAME or ${NAME}.
// This is deliberately the same grammar envsubst uses, since Substitute
// replaces it: bin/lib/tokens.sh restricts envsubst to exactly the PHI_*
// names the token files define, so any other $NAME or ${NAME} — a $PATH or
// $HOME inside a rendered config — is left untouched rather than rendered
// empty. A name not in tokens is therefore passed through unchanged, not
// dropped.
var variableRef = regexp.MustCompile(`\$\{[A-Za-z_][A-Za-z0-9_]*\}|\$[A-Za-z_][A-Za-z0-9_]*`)

// Substitute replaces every $NAME/${NAME} reference in src whose name is a
// key of tokens, leaving every other reference exactly as written.
func Substitute(tokens map[string]string, src []byte) []byte {
	return variableRef.ReplaceAllFunc(src, func(ref []byte) []byte {
		name := string(ref)
		switch {
		case name[1] == '{':
			name = name[2 : len(name)-1]
		default:
			name = name[1:]
		}
		if v, ok := tokens[name]; ok {
			return []byte(v)
		}
		return ref
	})
}
