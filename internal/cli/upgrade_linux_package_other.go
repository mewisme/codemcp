//go:build !linux

package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/install"
)

func runLinuxPackageManagedUpgrade(*cobra.Command, install.Detection, string, bool) error {
	return fmt.Errorf("native Linux package upgrades are unavailable on this platform")
}
