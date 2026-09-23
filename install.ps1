# CodeMCP bootstrap installer for Windows (PowerShell).
#
# irm https://get.mewis.me/chatgpt-mcp.ps1 | iex
#
# Environment:
#   CHATGPT_MCP_VERSION           release tag (default: latest)
#   CHATGPT_MCP_INSTALL_DIR       install location (default: %LOCALAPPDATA%\chatgpt-mcp)
#   CHATGPT_MCP_ARCH              architecture override: amd64 or arm64
#   INSTALL_ALLOW_CHECKSUM_ONLY   set to 1 to proceed when Sigstore/cosign
#                                 verification is unavailable (loud warning)

param(
  [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'
$repo = 'mewisme/codemcp'
$defaultInstall = Join-Path $env:LOCALAPPDATA 'chatgpt-mcp'
$installDir = if ($env:CHATGPT_MCP_INSTALL_DIR) { $env:CHATGPT_MCP_INSTALL_DIR } else { $defaultInstall }
$current = Join-Path $installDir 'current'
$oidcIssuer = 'https://token.actions.githubusercontent.com'
$signatureName = 'checksums.txt.sigstore.json'
$binaryName = 'cm.exe'

function ConvertTo-ChatGPTMCPArchitecture {
  param([AllowNull()][object]$Value)
  if ($null -eq $Value) { return $null }
  $text = ([string]$Value).Trim()
  if (-not $text) { return $null }
  switch -Regex ($text.ToUpperInvariant()) {
    '^(AMD64|X64|X86_64)$' { return 'amd64' }
    '^(ARM64|AARCH64)$' { return 'arm64' }
    'ARM.*64' { return 'arm64' }
    'INTEL64|AMD64' { return 'amd64' }
    default { return $null }
  }
}

function Get-ChatGPTMCPRuntimeArchitecture {
  try {
    $value = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    if ($null -ne $value) { return $value.ToString() }
  } catch {}
  return $null
}

function Get-ChatGPTMCPOSArchitecture {
  try {
    return (Get-CimInstance -ClassName Win32_OperatingSystem -ErrorAction Stop | Select-Object -First 1).OSArchitecture
  } catch {
    try { return (Get-WmiObject -Class Win32_OperatingSystem -ErrorAction Stop | Select-Object -First 1).OSArchitecture } catch {}
  }
  return $null
}

function Get-ChatGPTMCPProcessorMachineArchitecture {
  try {
    return (Get-CimInstance -ClassName Win32_Processor -ErrorAction Stop | Select-Object -First 1).Architecture
  } catch {
    try { return (Get-WmiObject -Class Win32_Processor -ErrorAction Stop | Select-Object -First 1).Architecture } catch {}
  }
  return $null
}

function Get-ChatGPTMCPRegistryProcessorIdentifier {
  try {
    return (Get-ItemProperty -Path 'HKLM:\HARDWARE\DESCRIPTION\System\CentralProcessor\0' -Name Identifier -ErrorAction Stop).Identifier
  } catch {}
  return $null
}

function Resolve-ChatGPTMCPArchitecture {
  param(
    [AllowNull()][string]$Override = $env:CHATGPT_MCP_ARCH,
    [AllowNull()][string]$RuntimeArchitecture = (Get-ChatGPTMCPRuntimeArchitecture),
    [AllowNull()][string]$ProcessorArchitectureW6432 = $env:PROCESSOR_ARCHITEW6432,
    [AllowNull()][string]$ProcessorArchitecture = $env:PROCESSOR_ARCHITECTURE,
    [AllowNull()][object]$ProcessorMachineArchitecture = (Get-ChatGPTMCPProcessorMachineArchitecture),
    [AllowNull()][string]$ProcessorIdentifier = $env:PROCESSOR_IDENTIFIER,
    [AllowNull()][string]$RegistryProcessorIdentifier = (Get-ChatGPTMCPRegistryProcessorIdentifier),
    [AllowNull()][string]$OSArchitecture = (Get-ChatGPTMCPOSArchitecture)
  )

  if ($Override) {
    $resolved = ConvertTo-ChatGPTMCPArchitecture $Override
    if ($resolved) { return $resolved }
    throw "cm: unsupported CHATGPT_MCP_ARCH '$Override'; expected amd64 or arm64."
  }

  foreach ($candidate in @($RuntimeArchitecture, $ProcessorArchitectureW6432, $ProcessorArchitecture, $ProcessorIdentifier, $RegistryProcessorIdentifier, $OSArchitecture)) {
    $resolved = ConvertTo-ChatGPTMCPArchitecture $candidate
    if ($resolved) { return $resolved }
  }

  switch ([string]$ProcessorMachineArchitecture) {
    '9' { return 'amd64' }
    '12' { return 'arm64' }
  }

  # Win32_OperatingSystem commonly reports only "64-bit" on x64 Windows.
  # Keep this as the final compatibility fallback after all architecture-specific probes.
  if ($OSArchitecture -match '^\s*64[ -]?bit\s*$') { return 'amd64' }

  $diagnostics = @(
    "runtime='$RuntimeArchitecture'",
    "PROCESSOR_ARCHITEW6432='$ProcessorArchitectureW6432'",
    "PROCESSOR_ARCHITECTURE='$ProcessorArchitecture'",
    "processorMachine='$ProcessorMachineArchitecture'",
    "PROCESSOR_IDENTIFIER='$ProcessorIdentifier'",
    "registryIdentifier='$RegistryProcessorIdentifier'",
    "OSArchitecture='$OSArchitecture'"
  ) -join ', '
  throw "cm: unsupported architecture; probes: $diagnostics. Set CHATGPT_MCP_ARCH=amd64 or arm64 to override."
}

function Test-ChatGPTMCPSafeArchivePath {
  param([Parameter(Mandatory = $true)][string]$Name)
  $normalized = $Name.Trim().Replace('\', '/')
  if (-not $normalized -or $normalized -eq '.' -or $normalized.StartsWith('/') -or $normalized.Contains(':')) {
    return $false
  }
  foreach ($segment in $normalized.Split('/')) {
    if ($segment -eq '..') { return $false }
  }
  return $true
}

function Expand-ChatGPTMCPBinaryFromZip {
  param(
    [Parameter(Mandatory = $true)][string]$ZipPath,
    [Parameter(Mandatory = $true)][string]$DestinationDir,
    [Parameter(Mandatory = $true)][string]$MemberName
  )

  Add-Type -AssemblyName System.IO.Compression
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $archive = [System.IO.Compression.ZipFile]::OpenRead($ZipPath)
  try {
    $matched = $null
    foreach ($entry in $archive.Entries) {
      $name = $entry.FullName
      if (-not (Test-ChatGPTMCPSafeArchivePath $name)) {
        throw "cm: unsafe archive path '$name'"
      }
      $normalized = $name.Trim().Replace('\', '/')
      if ($normalized.EndsWith('/')) { continue }

      $attrs = [int]($entry.ExternalAttributes -shr 16)
      if (($attrs -band 0xA000) -eq 0xA000) {
        throw "cm: refusing symlink archive member '$name'"
      }

      if ($normalized -ne $MemberName) { continue }
      if ($null -ne $matched) {
        throw "cm: release archive contains duplicate $MemberName"
      }
      $matched = $entry
    }
    if ($null -eq $matched) {
      throw "cm: $MemberName missing from archive."
    }
    if ($matched.Length -le 0) {
      throw "cm: release binary has invalid size $($matched.Length)"
    }

    New-Item -ItemType Directory -Force -Path $DestinationDir | Out-Null
    $destination = Join-Path $DestinationDir $MemberName
    $source = $matched.Open()
    try {
      $target = [System.IO.File]::Open($destination, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
      try {
        $source.CopyTo($target)
      } finally {
        $target.Dispose()
      }
    } finally {
      $source.Dispose()
    }
    return $destination
  } finally {
    $archive.Dispose()
  }
}

if ($Uninstall) {
  if (Test-Path $installDir) { Remove-Item -Recurse -Force $installDir }
  if ($installDir -eq $defaultInstall) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath) {
      $nextPath = (($userPath -split ';') | Where-Object { $_ -and $_ -ne $current }) -join ';'
      [Environment]::SetEnvironmentVariable('Path', $nextPath, 'User')
    }
  }
  Write-Host "CodeMCP uninstalled from $installDir"
  return
}

$arch = Resolve-ChatGPTMCPArchitecture

$version = $env:CHATGPT_MCP_VERSION
if (-not $version) {
  $version = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
}
if (-not $version) { throw 'cm: could not resolve latest version; set CHATGPT_MCP_VERSION.' }
if ($version -notmatch '^v') { $version = "v$version" }
$ver = $version.TrimStart('v')
$asset = "chatgpt-mcp_${ver}_windows_${arch}.zip"
$url = "https://github.com/$repo/releases/download/$version/$asset"
$checksumsUrl = "https://github.com/$repo/releases/download/$version/checksums.txt"
$signatureUrl = "https://github.com/$repo/releases/download/$version/$signatureName"
$certIdentity = "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$version"
Write-Host "Installing CodeMCP $version (windows/$arch)..."

$tmp = Join-Path $env:TEMP ("chatgpt-mcp-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
  $zip = Join-Path $tmp $asset
  $checksums = Join-Path $tmp 'checksums.txt'
  $signature = Join-Path $tmp $signatureName
  Invoke-WebRequest -Uri $url -OutFile $zip
  Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksums
  $expected = Get-Content $checksums | ForEach-Object {
    if ($_ -match '^([0-9a-fA-F]{64})\s+(.+)$' -and $Matches[2] -eq $asset) { $Matches[1].ToLowerInvariant() }
  } | Select-Object -First 1
  if (-not $expected) { throw "cm: checksum missing for $asset" }
  $actual = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLowerInvariant()
  if ($actual -ne $expected) { throw "cm: checksum verification failed for $asset" }

  $sigstoreOk = $false
  $signatureAvailable = $false
  try {
    Invoke-WebRequest -Uri $signatureUrl -OutFile $signature
    $signatureAvailable = $true
  } catch {
    $signatureAvailable = $false
  }

  $cosign = Get-Command cosign -ErrorAction SilentlyContinue
  if ($signatureAvailable -and $cosign) {
    & $cosign.Source verify-blob `
      --bundle=$signature `
      --certificate-identity=$certIdentity `
      --certificate-oidc-issuer=$oidcIssuer `
      $checksums
    if ($LASTEXITCODE -ne 0) {
      throw "cm: Sigstore/cosign verification failed for $signatureName"
    }
    $sigstoreOk = $true
    Write-Host 'Sigstore signature verified for checksums.txt.'
  }

  if (-not $sigstoreOk) {
    if ($env:INSTALL_ALLOW_CHECKSUM_ONLY -eq '1') {
      Write-Warning 'Sigstore/cosign verification unavailable; proceeding with checksum-only install because INSTALL_ALLOW_CHECKSUM_ONLY=1.'
      Write-Warning "Install cosign and ensure $signatureName is published for full release integrity."
    } else {
      $reason = if (-not $signatureAvailable) {
        "could not download $signatureName from $signatureUrl"
      } elseif (-not $cosign) {
        'cosign is not installed or not on PATH'
      } else {
        'Sigstore verification did not complete'
      }
      throw "cm: Sigstore/cosign verification is required but unavailable ($reason). Install cosign, or set INSTALL_ALLOW_CHECKSUM_ONLY=1 to proceed with checksum-only verification."
    }
  }

  $extract = Join-Path $tmp 'extract'
  $exe = Expand-ChatGPTMCPBinaryFromZip -ZipPath $zip -DestinationDir $extract -MemberName $binaryName

  & $exe install
  if ($LASTEXITCODE -ne 0) { throw "cm: self-install failed with exit code $LASTEXITCODE" }
} finally {
  if (Test-Path $tmp) { Remove-Item -Recurse -Force $tmp }
}

if ($installDir -eq $defaultInstall) {
  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $entries = if ($userPath) { $userPath -split ';' } else { @() }
  if ($entries -notcontains $current) {
    $nextPath = if ($userPath) { "$current;$userPath" } else { $current }
    [Environment]::SetEnvironmentVariable('Path', $nextPath, 'User')
    $env:Path = "$current;$env:Path"
    Write-Host "Added $current to your PATH (restart your terminal if needed)."
  }
} elseif (($env:Path -split ';') -notcontains $current) {
  Write-Host ''
  Write-Host "$current is not on your PATH. Add it to use cm from any terminal."
}

Write-Host ''
Write-Host 'Done. Run: cm --help'
