// Command rostr gives every coding-agent session a name drafted from a roster.
package main

import (
	"os"

	"github.com/piazzatron/rostr/cli/internal/app"
)

func main() {
	os.Exit(app.Execute())
}
