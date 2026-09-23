package update

import (
	"context"
	"fmt"
	"os"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

const githubActionsOIDCIssuer = "https://token.actions.githubusercontent.com"

func VerifyChecksumSignature(ctx context.Context, checksumPath, signaturePath, version string) error {
	span := tracepkg.Start(ctx, "UPDATE", "update.sigstore.verify", "Verifying Sigstore release signature", tracepkg.String("checksum", checksumPath), tracepkg.String("signature", signaturePath), tracepkg.String("version", version), tracepkg.String("oidc_issuer", githubActionsOIDCIssuer))
	version, err := NormalizeVersion(version)
	if err != nil {
		span.FailMessage("Sigstore release signature verification failed", err)
		return err
	}
	bundleSpan := tracepkg.Start(ctx, "UPDATE", "update.sigstore.bundle.load", "Loading Sigstore bundle", tracepkg.String("path", signaturePath))
	signedBundle, err := bundle.LoadJSONFromPath(signaturePath)
	if err != nil {
		bundleSpan.FailMessage("Sigstore bundle load failed", err)
		span.FailMessage("Sigstore release signature verification failed", err)
		return fmt.Errorf("load Sigstore bundle: %w", err)
	}
	bundleSpan.EndMessage("Sigstore bundle loaded")
	rootSpan := tracepkg.Start(ctx, "UPDATE", "update.sigstore.root.fetch", "Fetching Sigstore trusted root")
	trustedRoot, err := root.FetchTrustedRootWithOptions(tuf.DefaultOptions().WithContext(ctx))
	if err != nil {
		rootSpan.FailMessage("Sigstore trusted root fetch failed", err)
		span.FailMessage("Sigstore release signature verification failed", err)
		return fmt.Errorf("load Sigstore trusted root: %w", err)
	}
	rootSpan.EndMessage("Sigstore trusted root fetched")
	verifierSpan := tracepkg.Start(ctx, "UPDATE", "update.sigstore.verifier.create", "Creating Sigstore verifier")
	verifier, err := verify.NewVerifier(trustedRoot, verify.WithSignedCertificateTimestamps(1), verify.WithObserverTimestamps(1), verify.WithTransparencyLog(1))
	if err != nil {
		verifierSpan.FailMessage("Sigstore verifier creation failed", err)
		span.FailMessage("Sigstore release signature verification failed", err)
		return fmt.Errorf("create Sigstore verifier: %w", err)
	}
	verifierSpan.EndMessage("Sigstore verifier created")
	workflowIdentity := releaseWorkflowIdentity(version)
	identity, err := verify.NewShortCertificateIdentity(githubActionsOIDCIssuer, "", workflowIdentity, "")
	if err != nil {
		span.FailMessage("Sigstore release signature verification failed", err)
		return fmt.Errorf("create release signing identity: %w", err)
	}
	checksum, err := os.Open(checksumPath)
	if err != nil {
		span.FailMessage("Sigstore release signature verification failed", err)
		return err
	}
	defer checksum.Close()
	verifySpan := tracepkg.Start(ctx, "UPDATE", "update.sigstore.policy.verify", "Verifying Sigstore policy", tracepkg.String("oidc_issuer", githubActionsOIDCIssuer), tracepkg.String("workflow_identity", workflowIdentity))
	if _, err := verifier.Verify(signedBundle, verify.NewPolicy(verify.WithArtifact(checksum), verify.WithCertificateIdentity(identity))); err != nil {
		verifySpan.FailMessage("Sigstore policy verification failed", err)
		span.FailMessage("Sigstore release signature verification failed", err)
		return fmt.Errorf("sigstore verification failed: %w", err)
	}
	verifySpan.EndMessage("Sigstore policy verified")
	span.EndMessage("Sigstore release signature verified", tracepkg.String("workflow_identity", workflowIdentity))
	return nil
}

func releaseWorkflowIdentity(version string) string {
	return fmt.Sprintf("https://github.com/%s/%s/.github/workflows/release.yml@refs/tags/%s", DefaultOwner, DefaultRepo, version)
}
