// Command sapulidi removes what coding agents and toolchains leave behind.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/als0x/sapulidi/internal/cli"
)

func run(args []string, stdout, stderr io.Writer, euid func() int) int {
	if euid() == 0 {
		fmt.Fprintln(stderr, "sapulidi does not run as root")
		return 1
	}
	return cli.Main(args, stdout, stderr)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Geteuid))
}
