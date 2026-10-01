package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"go.mewis.me/codemcp/internal/productadapter"
)

func main() {
	out := flag.String("out", "frontend/src/lib/operation-presentation.generated.ts", "output path")
	check := flag.Bool("check", false, "verify output is current without writing")
	flag.Parse()

	data, err := productadapter.GeneratedTypeScript()
	if err != nil {
		fatal(err)
	}
	path := filepath.Clean(*out)
	if *check {
		current, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		if string(current) != string(data) {
			fatal(fmt.Errorf("generated presentation contract is stale: %s", path))
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
