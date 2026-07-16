// Command tarjan spins up a complete local development environment for a product
// — cloning repos, checking tools, and orchestrating services — from a single
// declarative tarjan.yaml.
package main

import "github.com/stevenzg/tarjan/cmd"

func main() {
	cmd.Execute()
}
