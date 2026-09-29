# CodeMCP bootstrap installer for Windows (PowerShell).
#
# irm https://get.mewis.me/codemcp.ps1 | iex
#
# Environment:
#   CM_VERSION           release tag (default: latest)
#   CM_INSTALL_DIR       install location (default: $HOME\.cm)
#   CM_ARCH              architecture override: amd64 or arm64

param(
  [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'
$repo = 'mewisme/codemcp'
$defaultInstall = Join-Path $HOME '.cm'
$installDir = if ($env:CM_INSTALL_DIR) { $env:CM_INSTALL_DIR } else { $defaultInstall }
$current = Join-Path $installDir 'current'
$oidcIssuer = 'https://token.actions.githubusercontent.com'
$packageName = 'codemcp'
$checksumName = "$packageName`_checksums.txt"
$signatureName = "$checksumName.sigstore.json"
$binaryName = 'cm.exe'
$maxArchiveEntries = 4096
$maxBinaryBytes = 268435456

function ConvertTo-CodeMCPArchitecture {
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

function Get-CodeMCPRuntimeArchitecture {
  try {
    $value = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    if ($null -ne $value) { return $value.ToString() }
  } catch {}
  return $null
}

function Get-CodeMCPOSArchitecture {
  try {
    return (Get-CimInstance -ClassName Win32_OperatingSystem -ErrorAction Stop | Select-Object -First 1).OSArchitecture
  } catch {
    try { return (Get-WmiObject -Class Win32_OperatingSystem -ErrorAction Stop | Select-Object -First 1).OSArchitecture } catch {}
  }
  return $null
}

function Get-CodeMCPProcessorMachineArchitecture {
  try {
    return (Get-CimInstance -ClassName Win32_Processor -ErrorAction Stop | Select-Object -First 1).Architecture
  } catch {
    try { return (Get-WmiObject -Class Win32_Processor -ErrorAction Stop | Select-Object -First 1).Architecture } catch {}
  }
  return $null
}

function Get-CodeMCPRegistryProcessorIdentifier {
  try {
    return (Get-ItemProperty -Path 'HKLM:\HARDWARE\DESCRIPTION\System\CentralProcessor\0' -Name Identifier -ErrorAction Stop).Identifier
  } catch {}
  return $null
}

function Resolve-CodeMCPArchitecture {
  param(
    [AllowNull()][string]$Override = $env:CM_ARCH,
    [AllowNull()][string]$RuntimeArchitecture = (Get-CodeMCPRuntimeArchitecture),
    [AllowNull()][string]$ProcessorArchitectureW6432 = $env:PROCESSOR_ARCHITEW6432,
    [AllowNull()][string]$ProcessorArchitecture = $env:PROCESSOR_ARCHITECTURE,
    [AllowNull()][object]$ProcessorMachineArchitecture = (Get-CodeMCPProcessorMachineArchitecture),
    [AllowNull()][string]$ProcessorIdentifier = $env:PROCESSOR_IDENTIFIER,
    [AllowNull()][string]$RegistryProcessorIdentifier = (Get-CodeMCPRegistryProcessorIdentifier),
    [AllowNull()][string]$OSArchitecture = (Get-CodeMCPOSArchitecture)
  )

  if ($Override) {
    $resolved = ConvertTo-CodeMCPArchitecture $Override
    if ($resolved) { return $resolved }
    throw "cm: unsupported CM_ARCH '$Override'; expected amd64 or arm64."
  }

  foreach ($candidate in @($RuntimeArchitecture, $ProcessorArchitectureW6432, $ProcessorArchitecture, $ProcessorIdentifier, $RegistryProcessorIdentifier, $OSArchitecture)) {
    $resolved = ConvertTo-CodeMCPArchitecture $candidate
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
  throw "cm: unsupported architecture; probes: $diagnostics. Set CM_ARCH=amd64 or arm64 to override."
}

function Test-CodeMCPSafeArchivePath {
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

function Expand-CodeMCPBinaryFromZip {
  param(
    [Parameter(Mandatory = $true)][string]$ZipPath,
    [Parameter(Mandatory = $true)][string]$DestinationDir,
    [Parameter(Mandatory = $true)][string]$MemberName
  )

  Add-Type -AssemblyName System.IO.Compression
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $archive = [System.IO.Compression.ZipFile]::OpenRead($ZipPath)
  try {
    if ($archive.Entries.Count -gt $maxArchiveEntries) {
      throw "cm: release archive exceeds $maxArchiveEntries entry limit"
    }
    $matched = $null
    foreach ($entry in $archive.Entries) {
      $name = $entry.FullName
      if (-not (Test-CodeMCPSafeArchivePath $name)) {
        throw "cm: unsafe archive path '$name'"
      }
      $normalized = $name.Trim().Replace('\', '/')
      if ($normalized.EndsWith('/')) { continue }

      $externalAttributes = [uint32](([int64]$entry.ExternalAttributes) -band 0xFFFFFFFFL)
      $unixType = [int](($externalAttributes -shr 16) -band 0xF000)
      $dosAttributes = [int]($externalAttributes -band 0xFFFF)
      if (($unixType -band 0xA000) -eq 0xA000) {
        throw "cm: refusing symlink archive member '$name'"
      }
      if (($dosAttributes -band 0x400) -ne 0) {
        throw "cm: refusing reparse archive member '$name'"
      }
      if ($unixType -ne 0 -and $unixType -ne 0x8000) {
        throw "cm: refusing non-regular archive member '$name'"
      }

      if ($normalized -ne $MemberName) { continue }
      if ($name -ne $MemberName) {
        throw "cm: release archive entrypoint must be exactly $MemberName"
      }
      if (($dosAttributes -band 0x10) -ne 0) {
        throw "cm: refusing non-regular archive member '$name'"
      }
      if ($null -ne $matched) {
        throw "cm: release archive contains duplicate $MemberName"
      }
      $matched = $entry
    }
    if ($null -eq $matched) {
      throw "cm: $MemberName missing from archive."
    }
    if ($matched.Length -le 0 -or $matched.Length -gt $maxBinaryBytes) {
      throw "cm: release binary has invalid size $($matched.Length)"
    }

    New-Item -ItemType Directory -Force -Path $DestinationDir | Out-Null
    $destination = Join-Path $DestinationDir $MemberName
    $source = $matched.Open()
    $target = $null
    $keep = $false
    try {
      $target = [System.IO.File]::Open($destination, [System.IO.FileMode]::CreateNew, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
      $buffer = New-Object byte[] 65536
      [long]$total = 0
      while (($read = $source.Read($buffer, 0, $buffer.Length)) -gt 0) {
        $total += $read
        if ($total -gt $maxBinaryBytes) {
          throw "cm: release binary exceeds $maxBinaryBytes byte limit"
        }
        $target.Write($buffer, 0, $read)
      }
      if ($total -le 0 -or $total -ne $matched.Length) {
        throw "cm: release binary size mismatch"
      }
      $target.Flush($true)
      $keep = $true
    } finally {
      if ($null -ne $target) { $target.Dispose() }
      $source.Dispose()
      if (-not $keep -and (Test-Path -LiteralPath $destination)) {
        Remove-Item -LiteralPath $destination -Force -ErrorAction SilentlyContinue
      }
    }
    return $destination
  } finally {
    $archive.Dispose()
  }
}

if ($Uninstall) {
  $candidate = Join-Path $current $binaryName
  if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
    throw "cm: installed CodeMCP executable not found; refusing to remove shared state under $installDir automatically."
  }
  & $candidate _service uninstall --external-cleanup
  if ($LASTEXITCODE -ne 0) { throw "cm: uninstall failed with exit code $LASTEXITCODE" }

  foreach ($ownedPath in @(
    (Join-Path $installDir 'current'),
    (Join-Path $installDir 'versions')
  )) {
    if (Test-Path -LiteralPath $ownedPath) { Remove-Item -LiteralPath $ownedPath -Recurse -Force }
  }
  foreach ($ownedFile in @(
    (Join-Path $installDir 'install.json'),
    (Join-Path (Join-Path $installDir 'state') 'update.json')
  )) {
    if (Test-Path -LiteralPath $ownedFile -PathType Leaf) { Remove-Item -LiteralPath $ownedFile -Force }
  }
  foreach ($emptyDir in @((Join-Path $installDir 'state'), $installDir)) {
    if (Test-Path -LiteralPath $emptyDir -PathType Container) {
      try { Remove-Item -LiteralPath $emptyDir -Force -ErrorAction Stop } catch {}
    }
  }
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

$arch = Resolve-CodeMCPArchitecture

$version = $env:CM_VERSION
if (-not $version) {
  $version = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
}
if (-not $version) { throw 'cm: could not resolve latest version; set CM_VERSION.' }
if ($version -notmatch '^v') { $version = "v$version" }
$ver = $version.TrimStart('v')
$asset = "${packageName}_${ver}_windows_${arch}.zip"
$url = "https://github.com/$repo/releases/download/$version/$asset"
$checksumsUrl = "https://github.com/$repo/releases/download/$version/$checksumName"
$signatureUrl = "https://github.com/$repo/releases/download/$version/$signatureName"
$certIdentity = "https://github.com/$repo/.github/workflows/release.yml@refs/tags/$version"
Write-Host "Installing CodeMCP $version (windows/$arch)..."

$tmp = Join-Path $env:TEMP ("cm-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
  $zip = Join-Path $tmp $asset
  $checksums = Join-Path $tmp $checksumName
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
  $sigstoreReason = $null
  try {
    Invoke-WebRequest -Uri $signatureUrl -OutFile $signature
  } catch {
    $sigstoreReason = "could not download $signatureName from $signatureUrl"
  }

  $cosign = Get-Command cosign -ErrorAction SilentlyContinue
  if (-not $sigstoreReason -and $cosign) {
    try {
      & $cosign.Source verify-blob `
        --bundle=$signature `
        --certificate-identity=$certIdentity `
        --certificate-oidc-issuer=$oidcIssuer `
        $checksums
      if ($LASTEXITCODE -eq 0) {
        $sigstoreOk = $true
        Write-Host "Sigstore signature verified for $checksumName."
      } else {
        $sigstoreReason = "Sigstore/cosign verification failed for $signatureName"
      }
    } catch {
      $sigstoreReason = "Sigstore/cosign verification failed for ${signatureName}: $($_.Exception.Message)"
    }
  } elseif (-not $sigstoreReason) {
    $sigstoreReason = 'cosign is not installed or not on PATH'
  }

  if (-not $sigstoreOk) {
    Write-Warning "$sigstoreReason; SHA-256 checksum verified, continuing without signature verification."
  }

  $extract = Join-Path $tmp 'extract'
  $exe = Expand-CodeMCPBinaryFromZip -ZipPath $zip -DestinationDir $extract -MemberName $binaryName

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
