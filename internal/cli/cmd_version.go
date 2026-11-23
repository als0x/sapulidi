package cli

import (
	"encoding/json"
	"fmt"

	"github.com/als0x/sapulidi/internal/version"
)

func init() {
	Register(Command{Name: "version", Summary: "print the version", Run: runVersion})
}

func runVersion(app *App, _ []string) int {
	if app.JSON {
		b, _ := json.Marshal(map[string]string{"version": version.Version})
		fmt.Fprintln(app.Stdout, string(b))
		return 0
	}
	fmt.Fprintln(app.Stdout, version.Version)
	return 0
}
