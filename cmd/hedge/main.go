// Command hedge keeps an estate's own names out of repositories that may
// not hold them. See README.md.
package main

import (
	"os"

	"github.com/orchard-suite/hedge/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
