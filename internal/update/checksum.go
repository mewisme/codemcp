package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tracepkg "go.mewis.me/codemcp/internal/trace"
)

var ErrChecksumMismatch = errors.New("release checksum mismatch")

func VerifyChecksum(archivePath, checksumPath, assetName string) error {
	return VerifyChecksumContext(context.Background(), archivePath, checksumPath, assetName)
}

func VerifyChecksumContext(ctx context.Context, archivePath, checksumPath, assetName string) error {
	span := tracepkg.Start(ctx, "UPDATE", "update.checksum.verify", "Verifying release checksum", tracepkg.String("archive", archivePath), tracepkg.String("checksum", checksumPath), tracepkg.String("asset", assetName))
	expected, err := expectedChecksum(checksumPath, assetName)
	if err != nil {
		span.FailMessage("Release checksum verification failed", err)
		return err
	}
	file, err := os.Open(archivePath)
	if err != nil {
		span.FailMessage("Release checksum verification failed", err)
		return err
	}
	defer file.Close()
	hash := sha256.New()
	bytesHashed, err := io.Copy(hash, file)
	if err != nil {
		span.FailMessage("Release checksum verification failed", err, tracepkg.Int64("bytes", bytesHashed))
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		err := fmt.Errorf("%w for %s", ErrChecksumMismatch, assetName)
		span.FailMessage("Release checksum verification failed", err, tracepkg.Int64("bytes", bytesHashed), tracepkg.Bool("match", false))
		return err
	}
	span.EndMessage("Release checksum verified", tracepkg.Int64("bytes", bytesHashed), tracepkg.Bool("match", true))
	return nil
}

func expectedChecksum(path, assetName string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != assetName {
			continue
		}
		hash := fields[0]
		if len(hash) != sha256.Size*2 {
			return "", fmt.Errorf("invalid checksum for %s", assetName)
		}
		if _, err := hex.DecodeString(hash); err != nil {
			return "", fmt.Errorf("invalid checksum for %s: %w", assetName, err)
		}
		return strings.ToLower(hash), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("checksum missing for %s", assetName)
}
