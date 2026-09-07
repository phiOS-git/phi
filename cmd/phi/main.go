// Command phi is the unified phiOS command-line interface.
package main

import (
	"os"

	"phi/internal/cli"
)

func main() {
	styled := cli.IsTerminal(os.Stdout)
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, styled))
}
