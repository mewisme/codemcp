[CmdletBinding()]
param(
  [string]$CompilerPath,
  [string]$FixtureBinaryPath,
  [string]$FailureBinaryPath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if (-not $IsWindows -and $env:OS -ne 'Windows_NT') {
  throw 'Windows setup smoke must run on Windows.'
}

function Get-UserPathState {
  $key = 'Registry::HKEY_CURRENT_USER\Environment'
  $properties = Get-ItemProperty -LiteralPath $key -ErrorAction SilentlyContinue
  $hasPath = $null -ne $properties -and $properties.PSObject.Properties.Name -contains 'Path'
  [pscustomobject]@{
    Exists = $hasPath
    Value = if ($hasPath) { [string]$properties.Path } else { $null }
  }
}

function Restore-UserPath([object]$State) {
  $key = 'Registry::HKEY_CURRENT_USER\Environment'
  if ($State.Exists) {
    Set-ItemProperty -LiteralPath $key -Name Path -Value $State.Value
  } else {
    Remove-ItemProperty -LiteralPath $key -Name Path -ErrorAction SilentlyContinue
  }
}

function Get-PathEntryCount([string]$PathValue, [string]$ExpectedEntry) {
  $expected = $ExpectedEntry.Trim().TrimEnd('\')
  if (-not $PathValue) { return 0 }
  return @(
    $PathValue -split ';' |
      Where-Object { $_.Trim().TrimEnd('\').Equals($expected, [StringComparison]::OrdinalIgnoreCase) }
  ).Count
}

function Invoke-Setup([string]$SetupPath, [string[]]$Arguments, [string]$LogPath, [int[]]$ExpectedExitCodes = @(0)) {
  $processArguments = @($Arguments)
  if ($LogPath) {
    $processArguments += "/LOG=$LogPath"
  }
  $process = Start-Process -FilePath $SetupPath -ArgumentList $processArguments -Wait -PassThru
  if ($ExpectedExitCodes -notcontains $process.ExitCode -and $LogPath -and (Test-Path -LiteralPath $LogPath -PathType Leaf)) {
    Write-Host "Inno Setup log tail ($LogPath):"
    Get-Content -LiteralPath $LogPath | Select-Object -Last 80 | ForEach-Object { Write-Host $_ }
  }
  return $process.ExitCode
}

function Assert-InstalledVersion([string]$InstallRoot, [string]$ExpectedVersion) {
  $installed = Join-Path $InstallRoot 'current\cm.exe'
  if (-not (Test-Path -LiteralPath $installed -PathType Leaf)) {
    throw "managed current binary missing at $installed"
  }
  $version = & $installed --version
  if ($LASTEXITCODE -ne 0 -or ($version -join [Environment]::NewLine) -notmatch [regex]::Escape($ExpectedVersion)) {
    throw "installed binary version mismatch: $($version -join ' ')"
  }
}

$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$contract = Get-Content -LiteralPath (Join-Path $repoRoot 'installer\windows\setup-contract.json') -Raw | ConvertFrom-Json
$builder = Join-Path $repoRoot 'scripts\release\build-windows-setup.ps1'
$silentSwitches = @($contract.ci.silent_switches | ForEach-Object { [string]$_ })

if (-not $CompilerPath) {
  $CompilerPath = $env:INNO_ISCC
}
if (-not $CompilerPath) {
  $command = Get-Command ([string]$contract.toolchain.compiler) -ErrorAction SilentlyContinue
  if ($command) { $CompilerPath = $command.Source }
}
if (-not $CompilerPath -or -not (Test-Path -LiteralPath $CompilerPath -PathType Leaf)) {
  throw 'pinned ISCC.exe is required for Windows setup smoke.'
}

$tempRoot = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { $env:TEMP }
if (-not $tempRoot) { throw 'temporary directory is unavailable.' }
$tmp = Join-Path $tempRoot ("cm-inno-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

$environmentNames = @('CM_INSTALL_DIR', 'CM_CONFIG_DIR', 'CM_TELEMETRY', 'CM_INSTALL_INTEGRATIONS', 'USERPROFILE')
$environmentBefore = @{}
foreach ($name in $environmentNames) {
  $environmentBefore[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
}
$userPathBefore = Get-UserPathState

try {
  $binary = Join-Path $tmp 'cm.exe'
  if ($FixtureBinaryPath) {
    Copy-Item -LiteralPath $FixtureBinaryPath -Destination $binary
  } else {
    $ldflags = '-s -w -X go.mewis.me/codemcp/internal/version.ReleaseTag=v9.9.9'
    & go build -trimpath -ldflags $ldflags -o $binary .
    if ($LASTEXITCODE -ne 0) { throw "fixture build failed with exit code $LASTEXITCODE" }
  }

  $successOutput = Join-Path $tmp 'success-output'
  $builderOutput = @(
    & $builder -BinaryPath $binary -Version 'v9.9.9' -OutputDirectory $successOutput -CompilerPath $CompilerPath
  )
  if ($LASTEXITCODE -ne 0) { throw "Inno setup build failed with exit code $LASTEXITCODE" }
  $setup = [string]$builderOutput[-1]
  $expectedSetup = Join-Path $successOutput ([string]$contract.target.artifact_name)
  if (-not $setup.Equals($expectedSetup, [StringComparison]::OrdinalIgnoreCase)) {
    throw "setup output name drifted: $setup"
  }
  if (-not (Test-Path -LiteralPath $setup -PathType Leaf)) {
    throw 'setup executable was not generated'
  }
  if (@(Get-ChildItem -LiteralPath $successOutput -File -Filter '*_setup.exe').Count -ne 1) {
    throw 'setup builder did not emit exactly one stable setup executable'
  }
  $digest = Join-Path $successOutput ([string]$contract.handoff.payload_digest_filename)
  if (-not (Test-Path -LiteralPath $digest -PathType Leaf)) {
    throw 'setup payload digest was not generated'
  }
  $digestText = [IO.File]::ReadAllText($digest)
  if ($digestText.Contains("`r") -or $digestText -notmatch '^[0-9a-f]{64}  cm\.exe\n$') {
    throw 'setup payload digest is not canonical LF-delimited sha256sum format'
  }

  $profileRoot = Join-Path $tmp 'profile'
  New-Item -ItemType Directory -Force -Path $profileRoot | Out-Null
  $defaultInstallRoot = Join-Path $profileRoot '.cm'
  $env:USERPROFILE = $profileRoot
  $env:CM_INSTALL_DIR = $defaultInstallRoot
  $env:CM_CONFIG_DIR = Join-Path $tmp 'config-default'
  $env:CM_TELEMETRY = '0'
  $env:CM_INSTALL_INTEGRATIONS = '0'

  $defaultExit = Invoke-Setup -SetupPath $setup -Arguments $silentSwitches -LogPath (Join-Path $tmp 'default-setup.log')
  if ($defaultExit -ne 0) { throw "default-root setup smoke failed with exit code $defaultExit" }
  Assert-InstalledVersion -InstallRoot $defaultInstallRoot -ExpectedVersion '9.9.9'

  $currentDir = Join-Path $defaultInstallRoot 'current'
  $pathAfterDefault = Get-UserPathState
  if ((Get-PathEntryCount -PathValue $pathAfterDefault.Value -ExpectedEntry $currentDir) -ne 1) {
    throw "default managed current directory was not registered exactly once in HKCU PATH: $currentDir"
  }

  $repeatExit = Invoke-Setup -SetupPath $setup -Arguments $silentSwitches -LogPath (Join-Path $tmp 'repeat-setup.log')
  if ($repeatExit -ne 0) { throw "repeat setup smoke failed with exit code $repeatExit" }
  $pathAfterRepeat = Get-UserPathState
  if ((Get-PathEntryCount -PathValue $pathAfterRepeat.Value -ExpectedEntry $currentDir) -ne 1) {
    throw 'default managed current directory PATH registration is not idempotent'
  }

  Restore-UserPath -State $userPathBefore
  $customInstallRoot = Join-Path $tmp 'custom-managed'
  $env:CM_INSTALL_DIR = $customInstallRoot
  $env:CM_CONFIG_DIR = Join-Path $tmp 'config-custom'

  $customExit = Invoke-Setup -SetupPath $setup -Arguments $silentSwitches -LogPath (Join-Path $tmp 'custom-setup.log')
  if ($customExit -ne 0) { throw "custom-root setup smoke failed with exit code $customExit" }
  Assert-InstalledVersion -InstallRoot $customInstallRoot -ExpectedVersion '9.9.9'
  $customCurrentDir = Join-Path $customInstallRoot 'current'
  $pathAfterCustom = Get-UserPathState
  if ((Get-PathEntryCount -PathValue $pathAfterCustom.Value -ExpectedEntry $customCurrentDir) -ne 1) {
    throw "custom managed current directory was not registered exactly once in HKCU PATH: $customCurrentDir"
  }

  $failureBinaryRoot = Join-Path $tmp 'failure-binary'
  New-Item -ItemType Directory -Force -Path $failureBinaryRoot | Out-Null
  $failureBinary = Join-Path $failureBinaryRoot 'cm.exe'
  if ($FailureBinaryPath) {
    Copy-Item -LiteralPath $FailureBinaryPath -Destination $failureBinary
  } else {
    $failureSource = Join-Path $tmp 'failure-fixture.go'
    $failureSourceContent = @'
package main

import "os"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install" {
		os.Exit(23)
	}
}
'@
    [IO.File]::WriteAllText($failureSource, $failureSourceContent, [Text.UTF8Encoding]::new($false))
    & go build -trimpath -o $failureBinary $failureSource
    if ($LASTEXITCODE -ne 0) { throw "failure fixture build failed with exit code $LASTEXITCODE" }
  }

  $failureOutput = Join-Path $tmp 'failure-output'
  $failureBuilderOutput = @(
    & $builder -BinaryPath $failureBinary -Version 'v9.9.9' -OutputDirectory $failureOutput -CompilerPath $CompilerPath
  )
  if ($LASTEXITCODE -ne 0) { throw "failure setup build failed with exit code $LASTEXITCODE" }
  $failureSetup = [string]$failureBuilderOutput[-1]

  $env:CM_INSTALL_DIR = Join-Path $tmp 'failure-managed'
  $env:CM_CONFIG_DIR = Join-Path $tmp 'config-failure'
  $failureExit = Invoke-Setup -SetupPath $failureSetup -Arguments $silentSwitches -LogPath (Join-Path $tmp 'failure-setup.log') -ExpectedExitCodes @(23)
  if ($failureExit -ne 23) {
    throw "delegated install failure exit code = $failureExit, want 23"
  }
  $pathAfterFailure = Get-UserPathState
  if ($pathAfterFailure.Exists -ne $userPathBefore.Exists -or $pathAfterFailure.Value -ne $userPathBefore.Value) {
    throw 'failed delegated install unexpectedly changed HKCU PATH'
  }
}
finally {
  Restore-UserPath -State $userPathBefore
  foreach ($name in $environmentNames) {
    [Environment]::SetEnvironmentVariable($name, $environmentBefore[$name], 'Process')
  }
  if (Test-Path -LiteralPath $tmp) {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }
}

Write-Host 'Windows Inno Setup smoke OK'
