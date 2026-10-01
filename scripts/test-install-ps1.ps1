$ErrorActionPreference = 'Stop'

$installer = Resolve-Path (Join-Path $PSScriptRoot '..\install.ps1')
$source = Get-Content -Raw -LiteralPath $installer
$tokens = $null
$errors = $null
$ast = [System.Management.Automation.Language.Parser]::ParseInput($source, [ref]$tokens, [ref]$errors)
if ($errors.Count -gt 0) {
  $errors | ForEach-Object { Write-Error $_ }
  exit 1
}

if ($source -match 'INSTALL_ALLOW_CHECKSUM_ONLY') {
  throw 'PowerShell installer still exposes the obsolete checksum-only opt-in.'
}
if ($source -match 'Sigstore/cosign verification is required') {
  throw 'PowerShell installer still blocks when Sigstore/cosign is unavailable.'
}
if ($source -notmatch 'SHA-256 checksum verified, continuing without signature verification') {
  throw 'PowerShell installer is missing the non-blocking checksum fallback warning.'
}
$sigstoreThrows = $ast.FindAll({
  param($candidate)
  $candidate -is [System.Management.Automation.Language.ThrowStatementAst] -and
    $candidate.Extent.Text -match '(?i)(sigstore|cosign)'
}, $true)
if ($sigstoreThrows.Count -gt 0) {
  throw 'PowerShell installer still has a blocking Sigstore/cosign throw path.'
}
if ($source -match 'Remove-Item\s+-Recurse\s+-Force\s+\$installDir') {
  throw 'PowerShell installer uninstall still recursively removes the shared CodeMCP root.'
}
if ($source -notmatch '_service\s+uninstall\s+--external-cleanup') {
  throw 'PowerShell installer uninstall does not delegate ownership checks to cm uninstall.'
}
if ($source -notmatch 'refusing to remove shared state under \$installDir automatically') {
  throw 'PowerShell installer uninstall is missing the fail-closed shared-state guard.'
}
if ($source -notmatch '&\s+\$exe\s+install') {
  throw 'PowerShell bootstrap no longer delegates installation to the downloaded canonical binary.'
}
$checksumGate = $source.IndexOf('if ($actual -ne $expected) { throw "cm: checksum verification failed for $asset" }')
$signatureDownload = $source.IndexOf('Invoke-WebRequest -Uri $signatureUrl -OutFile $signature')
if ($checksumGate -lt 0 -or $signatureDownload -lt 0 -or $checksumGate -gt $signatureDownload) {
  throw 'PowerShell bootstrap does not enforce SHA-256 before optional signature verification.'
}

$names = @(
  'ConvertTo-CodeMCPArchitecture',
  'Get-CodeMCPRuntimeArchitecture',
  'Get-CodeMCPOSArchitecture',
  'Get-CodeMCPProcessorMachineArchitecture',
  'Get-CodeMCPRegistryProcessorIdentifier',
  'Resolve-CodeMCPArchitecture',
  'Test-CodeMCPSafeArchivePath',
  'Expand-CodeMCPBinaryFromZip'
)
foreach ($name in $names) {
  $node = $ast.Find({ param($candidate) $candidate -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $candidate.Name -eq $name }, $true)
  if ($null -eq $node) { throw "missing installer function $name" }
  Invoke-Expression $node.Extent.Text
}

$maxArchiveEntries = 4096
$maxBinaryBytes = 268435456
$repoRoot = Split-Path -Parent $installer

function Get-ReleaseContract {
  param([string]$Architecture)
  Push-Location $repoRoot
  try {
    $lines = & go run ./scripts/release-layout-contract --os windows --arch $Architecture
    if ($LASTEXITCODE -ne 0) { throw "release-layout contract helper failed for windows/$Architecture" }
  } finally {
    Pop-Location
  }
  $values = @{}
  foreach ($line in $lines) {
    if ($line -match '^([^=]+)=(.*)$') { $values[$Matches[1]] = $Matches[2] }
  }
  return $values
}

foreach ($contractArch in @('amd64', 'arm64')) {
  $contract = Get-ReleaseContract $contractArch
  if (-not $contract.package -or -not $contract.checksum -or -not $contract.signature -or -not $contract.asset -or -not $contract.binary) {
    throw "canonical release contract is incomplete for windows/$contractArch"
  }
  if ($source -notmatch [regex]::Escape("$packageName = '$($contract.package)'")) {
    throw "PowerShell bootstrap package identity drifted from canonical release contract: $($contract.package)"
  }
  if ($contract.binary -ne 'cm.exe' -or $source -notmatch [regex]::Escape("$binaryName = 'cm.exe'")) {
    throw "PowerShell bootstrap binary identity drifted from canonical release contract: $($contract.binary)"
  }
  if ($contract.checksum -ne "$($contract.package)_checksums.txt" -or $contract.signature -ne "$($contract.checksum).sigstore.json") {
    throw "canonical release checksum/signature contract is internally inconsistent"
  }
  if ($contract.asset -ne "$($contract.package)_windows_${contractArch}.zip") {
    throw "canonical release asset tuple is unexpected: $($contract.asset)"
  }
}
if (-not $source.Contains('$asset = "${packageName}_windows_${arch}.zip"')) {
  throw 'PowerShell bootstrap asset naming formula drifted from canonical release contract.'
}
if (-not $source.Contains('$url = "https://github.com/$repo/releases/download/$version/$asset"')) {
  throw 'PowerShell bootstrap no longer pins archive downloads to the resolved release tag.'
}
if ($source.Contains('/releases/latest/download/')) {
  throw 'PowerShell bootstrap must resolve latest to a tag before downloading an artifact.'
}
$latestResolve = $source.IndexOf('(Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name')
$exactDownload = $source.IndexOf('$url = "https://github.com/$repo/releases/download/$version/$asset"')
if ($latestResolve -lt 0 -or $exactDownload -lt 0 -or $latestResolve -gt $exactDownload) {
  throw 'PowerShell bootstrap does not resolve a release tag before constructing the exact artifact URL.'
}
if (-not $source.Contains('$checksumName = "$packageName`_checksums.txt"') -or -not $source.Contains('$signatureName = "$checksumName.sigstore.json"')) {
  throw 'PowerShell bootstrap checksum/signature naming formula drifted from canonical release contract.'
}
$installFlowStart = $source.IndexOf('$arch = Resolve-CodeMCPArchitecture')
$installInvoke = $source.IndexOf('& $exe install', $installFlowStart)
if ($installFlowStart -lt 0 -or $installInvoke -lt 0) {
  throw 'PowerShell bootstrap install flow could not be identified.'
}
$preInstall = $source.Substring($installFlowStart, $installInvoke - $installFlowStart)
if ($preInstall -match 'Remove-Item[^\r\n]*\$installDir' -or $preInstall -match '\[System\.IO\.File\].*\$installDir') {
  throw 'PowerShell bootstrap mutates the working install before the downloaded Go install transaction starts.'
}

$fixtureRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("cm-archive-fixtures-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $fixtureRoot | Out-Null
try {
  foreach ($fixtureCase in @('valid', 'traversal', 'absolute', 'drive', 'duplicate', 'missing', 'symlink', 'reparse', 'nonregular', 'empty', 'aliased')) {
    $zipPath = Join-Path $fixtureRoot "$fixtureCase.zip"
    Push-Location $repoRoot
    try {
      & go run ./scripts/release-archive-fixture --format zip --case $fixtureCase --output $zipPath
      if ($LASTEXITCODE -ne 0) { throw "failed to generate ZIP fixture $fixtureCase" }
    } finally {
      Pop-Location
    }
    $destination = Join-Path $fixtureRoot "extract-$fixtureCase"
    if ($fixtureCase -eq 'valid') {
      $extracted = Expand-CodeMCPBinaryFromZip -ZipPath $zipPath -DestinationDir $destination -MemberName 'cm.exe'
      if (-not (Test-Path -LiteralPath $extracted -PathType Leaf) -or (Get-Item -LiteralPath $extracted).Length -le 0) {
        throw 'PowerShell archive validator failed the valid canonical fixture.'
      }
      continue
    }
    $failed = $false
    try {
      Expand-CodeMCPBinaryFromZip -ZipPath $zipPath -DestinationDir $destination -MemberName 'cm.exe' | Out-Null
    } catch {
      $failed = $true
    }
    if (-not $failed) {
      throw "PowerShell archive validator accepted unsafe fixture '$fixtureCase'."
    }
  }
} finally {
  if (Test-Path -LiteralPath $fixtureRoot) { Remove-Item -LiteralPath $fixtureRoot -Recurse -Force }
}

function Assert-Architecture {
  param([string]$Expected, [hashtable]$Arguments)
  $actual = Resolve-CodeMCPArchitecture @Arguments
  if ($actual -ne $Expected) { throw "architecture mismatch: expected '$Expected', got '$actual' for $($Arguments | Out-String)" }
}

function Merge-Arguments {
  param([hashtable]$Base, [hashtable]$Override)
  $result = @{}
  foreach ($key in $Base.Keys) { $result[$key] = $Base[$key] }
  foreach ($key in $Override.Keys) { $result[$key] = $Override[$key] }
  return $result
}

$empty = @{
  Override = ''
  RuntimeArchitecture = ''
  ProcessorArchitectureW6432 = ''
  ProcessorArchitecture = ''
  ProcessorMachineArchitecture = $null
  ProcessorIdentifier = ''
  RegistryProcessorIdentifier = ''
  OSArchitecture = ''
}

Assert-Architecture 'amd64' (Merge-Arguments $empty @{ RuntimeArchitecture = 'X64' })
Assert-Architecture 'arm64' (Merge-Arguments $empty @{ RuntimeArchitecture = 'Arm64' })
Assert-Architecture 'amd64' (Merge-Arguments $empty @{ ProcessorArchitectureW6432 = 'AMD64' })
Assert-Architecture 'arm64' (Merge-Arguments $empty @{ ProcessorArchitecture = 'ARM64' })
Assert-Architecture 'amd64' (Merge-Arguments $empty @{ ProcessorMachineArchitecture = 9; OSArchitecture = '64-bit' })
Assert-Architecture 'arm64' (Merge-Arguments $empty @{ ProcessorMachineArchitecture = 12; OSArchitecture = '64-bit' })
Assert-Architecture 'amd64' (Merge-Arguments $empty @{ ProcessorIdentifier = 'Intel64 Family 6 Model 154 Stepping 3, GenuineIntel' })
Assert-Architecture 'arm64' (Merge-Arguments $empty @{ RegistryProcessorIdentifier = 'ARMv8 (64-bit) Family 8 Model 1 Revision 0' })
Assert-Architecture 'amd64' (Merge-Arguments $empty @{ OSArchitecture = '64-bit' })
Assert-Architecture 'arm64' (Merge-Arguments $empty @{ Override = 'aarch64' })
Assert-Architecture 'amd64' (Merge-Arguments $empty @{ Override = 'x64' })

$failed = $false
$invalidOverride = Merge-Arguments $empty @{ Override = 'x86' }
try { Resolve-CodeMCPArchitecture @invalidOverride | Out-Null } catch { $failed = $_.Exception.Message -match 'CM_ARCH' }
if (-not $failed) { throw 'invalid architecture override was accepted' }

$failed = $false
try { Resolve-CodeMCPArchitecture @empty | Out-Null } catch { $failed = $_.Exception.Message -match "runtime=''" -and $_.Exception.Message -match 'OSArchitecture' }
if (-not $failed) { throw 'unsupported architecture did not include probe diagnostics' }

Write-Host 'PowerShell installer architecture tests passed.'
