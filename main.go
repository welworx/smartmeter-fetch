// Command smartmeter-fetch fetches smart meter readings from grid operator
// web portals, stores them locally, and serves them over a small HTTP API.
//
// Not yet implemented — this is repo scaffolding only.
package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	fmt.Fprintf(os.Stderr, "smartmeter-fetch %s: not yet implemented\n", version)
	os.Exit(1)
}
