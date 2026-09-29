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

$names = @(
  'ConvertTo-CodeMCPArchitecture',
  'Get-CodeMCPRuntimeArchitecture',
  'Get-CodeMCPOSArchitecture',
  'Get-CodeMCPProcessorMachineArchitecture',
  'Get-CodeMCPRegistryProcessorIdentifier',
  'Resolve-CodeMCPArchitecture'
)
foreach ($name in $names) {
  $node = $ast.Find({ param($candidate) $candidate -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $candidate.Name -eq $name }, $true)
  if ($null -eq $node) { throw "missing installer function $name" }
  Invoke-Expression $node.Extent.Text
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
