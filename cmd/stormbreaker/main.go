// Command stormbreaker serves the orchestrator and dispatchers of operational acceptance tests, and
// works with streams.
package main

import (
	"os"

	"github.com/nimbusxr/stormbreaker/internal/cli"
)

func main() { os.Exit(cli.Main(os.Args[1:])) }
