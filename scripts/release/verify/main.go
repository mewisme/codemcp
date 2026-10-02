package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/releaseverify"
)

func main() {
	repositoryRoot := flag.String("repository-root", "", "verify repository release contracts from this root")
	githubRepository := flag.String("github-repository", "", "observed GitHub owner/repository")
	vanityURL := flag.String("vanity-url", "", "verify Go vanity metadata at this URL")
	binary := flag.String("binary", "", "verify telemetry metadata from this binary")
	dist := flag.String("dist", "", "verify GoReleaser artifacts and package manifests")
	expectTelemetry := flag.String("expect-telemetry", "", "telemetry expectation: absent or present")
	telemetryEndpoint := flag.Bool("telemetry-endpoint", false, "verify TELEMETRY_ENDPOINT release metadata")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runs := 0
	if strings.TrimSpace(*repositoryRoot) != "" {
		runs++
		if err := releaseverify.VerifyRepository(*repositoryRoot, *githubRepository); err != nil {
			fatal(err)
		}
		fmt.Println("release repository contracts verified")
	}
	if strings.TrimSpace(*vanityURL) != "" {
		runs++
		if err := releaseverify.VerifyVanity(ctx, nil, *vanityURL); err != nil {
			fatal(err)
		}
		fmt.Println("Go vanity metadata verified")
	}
	expectation := releaseverify.TelemetryExpectation(strings.TrimSpace(*expectTelemetry))
	if *telemetryEndpoint {
		runs++
		if err := releaseverify.VerifyTelemetryEndpoint(os.Getenv("TELEMETRY_ENDPOINT")); err != nil {
			fatal(err)
		}
		fmt.Println("release telemetry endpoint metadata verified")
	}
	if strings.TrimSpace(*binary) != "" {
		runs++
		if err := releaseverify.VerifyBinaryTelemetry(ctx, *binary, expectation); err != nil {
			fatal(err)
		}
		fmt.Println("release binary telemetry boundary verified")
	}
	if strings.TrimSpace(*dist) != "" {
		runs++
		if err := releaseverify.VerifyDist(ctx, *dist, expectation); err != nil {
			fatal(err)
		}
		fmt.Println("release artifacts and package manifests verified")
	}
	if runs == 0 {
		fatal(fmt.Errorf("no release verification target selected"))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "release verification failed:", err)
	os.Exit(1)
}
