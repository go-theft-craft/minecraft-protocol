// Command apibaseline records the parent module's public surface as the
// compatibility baseline. Run it through `task api:accept`, deliberately, in
// the same commit as the change it accepts.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/go-theft-craft/minecraft-protocol/apicompat/apicheck"
)

func main() {
	dir := flag.String("module", "..", "root of the module whose surface is frozen")
	out := flag.String("out", "../api", "where the baseline is written")
	flag.Parse()

	surface, fset, err := apicheck.Load(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := apicheck.WriteBaseline(*out, surface, fset); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("froze %d packages\n", len(surface))
}
