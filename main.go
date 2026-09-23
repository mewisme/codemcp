package main

import (
	"os"

	"go.mewis.me/codemcp/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
