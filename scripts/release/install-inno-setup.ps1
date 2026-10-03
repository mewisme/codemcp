[CmdletBinding()]
param(
  [string]$InstallRoot
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Throw-InstallError([string]$Message) {
  throw "inno setup toolchain: $Message"
}

$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$contractPath = Join-Path $repoRoot 'installer/windows/setup-contract.json'
$contract = Get-Content -LiteralPath $contractPath -Raw | ConvertFrom-Json
$version = [string]$contract.toolchain.version
$compilerName = [string]$contract.toolchain.compiler

if ([string]::IsNullOrWhiteSpace($version) -or $compilerName -ne 'ISCC.exe') {
  Throw-InstallError 'toolchain contract is invalid'
}

$releaseID = $version.Replace('.', '_')
$assetName = "innosetup-$version-x64.exe"
$downloadURI = "https://github.com/jrsoftware/issrc/releases/download/is-$releaseID/$assetName"

$tempRoot = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { $env:TEMP }
if (-not $tempRoot) {
  Throw-InstallError 'temporary directory is unavailable'
}

if (-not $InstallRoot) {
  $InstallRoot = Join-Path $tempRoot "inno-$version"
}
$installPath = [IO.Path]::GetFullPath($InstallRoot)
if (Test-Path -LiteralPath $installPath) {
  $existing = Get-Item -LiteralPath $installPath -Force
  if (-not $existing.PSIsContainer -or ($existing.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    Throw-InstallError "install root must be a regular directory: $installPath"
  }
} else {
  New-Item -ItemType Directory -Path $installPath -Force | Out-Null
}

$installer = Join-Path $tempRoot $assetName
try {
  Invoke-WebRequest -UseBasicParsing -Uri $downloadURI -OutFile $installer
  if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) {
    Throw-InstallError "downloaded installer is missing: $installer"
  }

  $process = Start-Process -FilePath $installer -ArgumentList @(
    '/PORTABLE=1',
    '/VERYSILENT',
    '/SUPPRESSMSGBOXES',
    '/NORESTART',
    '/NOICONS',
    '/SP-',
    "/DIR=$installPath"
  ) -Wait -PassThru
  if ($process.ExitCode -ne 0) {
    Throw-InstallError "portable install failed with exit code $($process.ExitCode)"
  }

  $compiler = Join-Path $installPath $compilerName
  if (-not (Test-Path -LiteralPath $compiler -PathType Leaf)) {
    Throw-InstallError "compiler was not installed at $compiler"
  }
  $compilerItem = Get-Item -LiteralPath $compiler -Force
  if (($compilerItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    Throw-InstallError "compiler must not be a symlink or reparse point: $compiler"
  }

  $observed = (& $compiler '--version' 2>&1 | Out-String).Trim()
  if ($LASTEXITCODE -ne 0 -or $observed -ne $version) {
    Throw-InstallError "compiler version $observed does not match pinned version $version"
  }

  Write-Output $compilerItem.FullName
}
finally {
  Remove-Item -LiteralPath $installer -Force -ErrorAction SilentlyContinue
}
