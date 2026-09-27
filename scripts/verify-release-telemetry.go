package main

import (
	"fmt"
	"os"

	producttelemetry "go.mewis.me/codemcp/internal/telemetry/product"
)

func main() {
	metadata, err := producttelemetry.ParseEndpoint(os.Getenv("TELEMETRY_ENDPOINT"))
	if err != nil || !metadata.Available || metadata.Product != "codemcp" {
		fmt.Fprintln(os.Stderr, "release telemetry endpoint metadata is missing or invalid")
		os.Exit(1)
	}
	fmt.Println("release telemetry endpoint metadata verified")
}
