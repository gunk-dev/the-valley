package main

import (
	"os"

	"the-valley/flow/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(cmd.ExitCode(err))
	}
}
