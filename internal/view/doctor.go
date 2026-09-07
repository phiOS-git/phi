package view

import (
	"fmt"
	"strings"

	"phi/internal/doctor"
)

// Doctor renders one `phi doctor` report: one block per check, plain text —
// the same script-parseable shape phi theme check and phi state list use.
// It never varies with styled: doctor's whole point is to be output a
// person and a systemd timer can both trust, so there is no decoration to
// drop when redirected in the first place.
func Doctor(r doctor.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "phi doctor — %s\n", r.Host)

	problems := 0
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "\n[%s] %s\n", c.Status, c.Name)
		for _, line := range strings.Split(c.Detail, "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		if c.Status == doctor.Problem {
			problems++
		}
	}

	fmt.Fprintf(&b, "\n%d check(s), %d problem(s)\n", len(r.Checks), problems)
	return b.String()
}
