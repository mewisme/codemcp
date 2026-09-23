package install

import (
	"context"
	"errors"
	"os"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

func RollbackResult(result Result) error {
	return RollbackResultContext(context.Background(), result)
}

func RollbackResultContext(ctx context.Context, result Result) error {
	span := tracepkg.Start(ctx, "INSTALL", "install.result.rollback", "Rolling back installed result", tracepkg.String("version", result.Version), tracepkg.String("previous_version", result.Activation.PreviousVersion))
	if err := Rollback(result.Activation); err != nil {
		span.FailMessage("Installed result activation rollback failed", err)
		return err
	}
	if result.PreviousMetadata != nil {
		if err := WriteMetadata(result.Layout.Metadata, *result.PreviousMetadata); err != nil {
			span.FailMessage("Installed result metadata rollback failed", err, tracepkg.Bool("metadata_restored", false))
			return err
		}
		span.EndMessage("Installed result rolled back", tracepkg.Bool("activation_restored", true), tracepkg.Bool("metadata_restored", true))
		return nil
	}
	if err := os.Remove(result.Layout.Metadata); err != nil && !errors.Is(err, os.ErrNotExist) {
		span.FailMessage("Installed result metadata cleanup failed", err, tracepkg.Bool("metadata_removed", false))
		return err
	}
	span.EndMessage("Installed result rolled back", tracepkg.Bool("activation_restored", true), tracepkg.Bool("metadata_removed", true))
	return nil
}

func FinalizeResult(result Result) error {
	return FinalizeResultContext(context.Background(), result)
}

func FinalizeResultContext(ctx context.Context, result Result) error {
	keep := []string{result.Version}
	if previous := result.Activation.PreviousVersion; previous != "" && previous != result.Version {
		keep = append(keep, previous)
	}
	span := tracepkg.Start(ctx, "INSTALL", "install.versions.cleanup", "Cleaning old installed versions", tracepkg.Any("keep_versions", append([]string(nil), keep...)), tracepkg.String("versions_dir", result.Layout.Versions))
	before := versionDirectoryCount(result.Layout.Versions)
	if err := Cleanup(result.Layout, keep...); err != nil {
		span.FailMessage("Old installed version cleanup failed", err, tracepkg.Int("before_count", before))
		return err
	}
	after := versionDirectoryCount(result.Layout.Versions)
	removed := before - after
	if removed < 0 {
		removed = 0
	}
	span.EndMessage("Old installed versions cleaned", tracepkg.Int("before_count", before), tracepkg.Int("after_count", after), tracepkg.Int("removed_count", removed), tracepkg.Any("keep_versions", append([]string(nil), keep...)))
	return nil
}

func versionDirectoryCount(root string) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".staging-") {
			count++
		}
	}
	return count
}
