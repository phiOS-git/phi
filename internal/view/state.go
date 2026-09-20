package view

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"phi/internal/state"
)

// StateList renders `phi state list`: every defined key in plain aligned
// columns. JSON output is left for whichever consumer first needs it.
func StateList(entries []state.Entry) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "KEY\tVALUE\n")
	for _, e := range entries {
		value := e.Value
		if !e.Set {
			value = "(unset)"
		}
		fmt.Fprintf(tw, "%s\t%s\n", e.Key, value)
	}
	tw.Flush()
	return b.String()
}
