package main

import (
	"flag"
	"fmt"
	"os"

	updatepkg "go.mewis.me/codemcp/internal/update"
)

func main() {
	version := flag.String("version", "v9.9.9", "release version")
	goos := flag.String("os", "", "target operating system")
	goarch := flag.String("arch", "", "target architecture")
	flag.Parse()

	layout := updatepkg.PrimaryReleaseLayout()
	fmt.Printf("package=%s\n", layout.PackageName)
	fmt.Printf("checksum=%s\n", layout.ChecksumName)
	fmt.Printf("signature=%s\n", layout.SignatureName)
	for _, platform := range layout.Platforms {
		fmt.Printf("platform=%s/%s|%s|%s\n", platform.OS, platform.Arch, platform.ArchiveExtension, platform.BinaryName)
	}
	if *goos == "" && *goarch == "" {
		return
	}
	asset, err := updatepkg.AssetName(*version, *goos, *goarch)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary, err := updatepkg.BinaryName(*goos, *goarch)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("asset=%s\n", asset)
	fmt.Printf("binary=%s\n", binary)
}
