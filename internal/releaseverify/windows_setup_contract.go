package releaseverify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	updatepkg "go.mewis.me/codemcp/internal/update"
)

const windowsSetupContractPath = "installer/windows/setup-contract.json"

type windowsSetupContract struct {
	Schema    int                           `json:"schema"`
	Toolchain windowsSetupToolchainContract `json:"toolchain"`
	Target    windowsSetupTargetContract    `json:"target"`
	Script    windowsSetupScriptContract    `json:"script"`
	CI        windowsSetupCIContract        `json:"ci"`
	Handoff   windowsSetupHandoffContract   `json:"handoff"`
	Publish   windowsSetupPublishContract   `json:"publication"`
	Limits    windowsSetupLimitsContract    `json:"constraints"`
}

type windowsSetupToolchainContract struct {
	Compiler  string `json:"compiler"`
	PackageID string `json:"package_id"`
	Version   string `json:"version"`
	Edition   string `json:"edition"`
}

type windowsSetupTargetContract struct {
	OS                   string `json:"os"`
	Arch                 string `json:"arch"`
	BinaryName           string `json:"binary_name"`
	ArtifactName         string `json:"artifact_name"`
	SetupArchitecture    string `json:"setup_architecture"`
	ArchitecturesAllowed string `json:"architectures_allowed"`
}

type windowsSetupScriptContract struct {
	Path               string                  `json:"path"`
	Defines            windowsSetupDefineNames `json:"defines"`
	PrivilegesRequired string                  `json:"privileges_required"`
	CreateAppDir       bool                    `json:"create_app_dir"`
	Uninstallable      bool                    `json:"uninstallable"`
	AddRemovePrograms  bool                    `json:"add_remove_programs"`
	PayloadCount       int                     `json:"payload_count"`
	DelegateCommand    string                  `json:"delegate_command"`
	PathScope          string                  `json:"path_scope"`
}

type windowsSetupDefineNames struct {
	BinaryPath         string `json:"binary_path"`
	SetupVersion       string `json:"setup_version"`
	OutputDir          string `json:"output_dir"`
	OutputBaseFilename string `json:"output_base_filename"`
	TargetArch         string `json:"target_arch"`
}

type windowsSetupCIContract struct {
	SilentSwitches []string `json:"silent_switches"`
}

type windowsSetupHandoffContract struct {
	WindowsJob             string `json:"windows_job"`
	LinuxJob               string `json:"linux_job"`
	WorkflowArtifact       string `json:"workflow_artifact"`
	StagingDirectory       string `json:"staging_directory"`
	SetupFilename          string `json:"setup_filename"`
	PayloadDigestFilename  string `json:"payload_digest_filename"`
	PayloadDigestAlgorithm string `json:"payload_digest_algorithm"`
	ArchiveFilename        string `json:"archive_filename"`
	ArchivePayloadPath     string `json:"archive_payload_path"`
}

type windowsSetupPublishContract struct {
	ChecksumAuthority           string `json:"checksum_authority"`
	SignatureAuthority          string `json:"signature_authority"`
	ReleaseAuthority            string `json:"release_authority"`
	WindowsJobPublishesDirectly bool   `json:"windows_job_publishes_directly"`
}

type windowsSetupLimitsContract struct {
	WineAllowed                         bool `json:"wine_allowed"`
	GoReleaserProRequired               bool `json:"goreleaser_pro_required"`
	AuthenticodeRequired                bool `json:"authenticode_required"`
	CommercialLicenseComplianceExternal bool `json:"commercial_license_compliance_external"`
	CILicenseKeyRequired                bool `json:"ci_license_key_required"`
}

func verifyWindowsSetupContract(root string) error {
	contract, err := loadWindowsSetupContract(root)
	if err != nil {
		return err
	}
	if contract.Schema != 1 {
		return fmt.Errorf("windows setup contract schema = %d, want 1", contract.Schema)
	}
	if contract.Toolchain != (windowsSetupToolchainContract{
		Compiler: "ISCC.exe", PackageID: "JRSoftware.InnoSetup.7", Version: "7.1.0", Edition: "x64",
	}) {
		return fmt.Errorf("windows setup toolchain contract drifted: %#v", contract.Toolchain)
	}

	setupName, err := updatepkg.ArtifactName(updatepkg.ArtifactSetup, "windows", "amd64")
	if err != nil {
		return err
	}
	archiveName, err := updatepkg.ArchiveName("windows", "amd64")
	if err != nil {
		return err
	}
	if contract.Target != (windowsSetupTargetContract{
		OS: "windows", Arch: "amd64", BinaryName: "cm.exe", ArtifactName: setupName,
		SetupArchitecture: "x64", ArchitecturesAllowed: "x64compatible",
	}) {
		return fmt.Errorf("windows setup target contract drifted: %#v", contract.Target)
	}
	if _, ok := updatepkg.ArtifactFor(updatepkg.ArtifactSetup, "windows", "arm64"); ok {
		return errors.New("windows setup release layout unexpectedly supports windows/arm64")
	}

	if contract.Script != (windowsSetupScriptContract{
		Path: "installer/windows/codemcp.iss",
		Defines: windowsSetupDefineNames{
			BinaryPath: "BinaryPath", SetupVersion: "SetupVersion", OutputDir: "OutputDir",
			OutputBaseFilename: "OutputBaseFilename", TargetArch: "TargetArch",
		},
		PrivilegesRequired: "lowest",
		CreateAppDir:       false,
		Uninstallable:      false,
		AddRemovePrograms:  false,
		PayloadCount:       1,
		DelegateCommand:    "cm.exe install",
		PathScope:          "hkcu-default-managed-root-only",
	}) {
		return fmt.Errorf("windows setup script contract drifted: %#v", contract.Script)
	}
	if !slices.Equal(contract.CI.SilentSwitches, []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-"}) {
		return fmt.Errorf("windows setup silent switches drifted: %v", contract.CI.SilentSwitches)
	}
	if contract.Handoff != (windowsSetupHandoffContract{
		WindowsJob: "windows-setup", LinuxJob: "release", WorkflowArtifact: "codemcp-windows-setup-staging",
		StagingDirectory: ".release-staging/windows-setup", SetupFilename: setupName,
		PayloadDigestFilename: "codemcp_windows_amd64_cm.sha256", PayloadDigestAlgorithm: "sha256",
		ArchiveFilename: archiveName, ArchivePayloadPath: "cm.exe",
	}) {
		return fmt.Errorf("windows setup release handoff contract drifted: %#v", contract.Handoff)
	}
	if contract.Publish != (windowsSetupPublishContract{
		ChecksumAuthority: "goreleaser", SignatureAuthority: "goreleaser", ReleaseAuthority: "goreleaser",
		WindowsJobPublishesDirectly: false,
	}) {
		return fmt.Errorf("windows setup publication authority drifted: %#v", contract.Publish)
	}
	if contract.Limits != (windowsSetupLimitsContract{
		WineAllowed: false, GoReleaserProRequired: false, AuthenticodeRequired: false,
		CommercialLicenseComplianceExternal: true, CILicenseKeyRequired: false,
	}) {
		return fmt.Errorf("windows setup build constraints drifted: %#v", contract.Limits)
	}
	return nil
}

func loadWindowsSetupContract(root string) (windowsSetupContract, error) {
	path := filepath.Join(root, filepath.FromSlash(windowsSetupContractPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return windowsSetupContract{}, fmt.Errorf("read Windows setup contract: %w", err)
	}
	if len(data) == 0 || len(data) > 64<<10 {
		return windowsSetupContract{}, errors.New("windows setup contract must be a bounded non-empty file")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var contract windowsSetupContract
	if err := decoder.Decode(&contract); err != nil {
		return windowsSetupContract{}, fmt.Errorf("decode Windows setup contract: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return windowsSetupContract{}, errors.New("windows setup contract contains trailing JSON values")
	}
	return contract, nil
}
