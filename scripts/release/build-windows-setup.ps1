[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$BinaryPath,

  [Parameter(Mandatory = $true)]
  [string]$Version,

  [Parameter(Mandatory = $true)]
  [string]$OutputDirectory,

  [ValidateSet('amd64')]
  [string]$TargetArch = 'amd64',

  [string]$CompilerPath
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Throw-BuildError([string]$Message) {
  throw "windows setup: $Message"
}

function Assert-RegularFile([string]$Path, [string]$Label) {
  if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
    Throw-BuildError "$Label must be a regular file: $Path"
  }
  $item = Get-Item -LiteralPath $Path -Force
  if (($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
    Throw-BuildError "$Label must not be a symlink or reparse point: $Path"
  }
  return $item
}

function Resolve-Compiler([string]$RequestedPath, [string]$ExpectedName) {
  if ($RequestedPath) {
    return (Assert-RegularFile -Path $RequestedPath -Label 'compiler').FullName
  }

  $command = Get-Command $ExpectedName -ErrorAction SilentlyContinue
  if ($command -and $command.Source) {
    return (Assert-RegularFile -Path $command.Source -Label 'compiler').FullName
  }

  if ($env:ProgramFiles) {
    $candidate = Join-Path $env:ProgramFiles 'Inno Setup 7\ISCC.exe'
    if (Test-Path -LiteralPath $candidate -PathType Leaf) {
      return (Assert-RegularFile -Path $candidate -Label 'compiler').FullName
    }
  }

  Throw-BuildError "$ExpectedName is required"
}

$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$contractPath = Join-Path $repoRoot 'installer/windows/setup-contract.json'
$contract = Get-Content -LiteralPath $contractPath -Raw | ConvertFrom-Json

if ($contract.target.os -ne 'windows' -or $contract.target.arch -ne $TargetArch) {
  Throw-BuildError "unsupported target windows/$TargetArch"
}
if ($contract.target.binary_name -ne 'cm.exe') {
  Throw-BuildError 'setup contract binary name drifted'
}

$binary = Assert-RegularFile -Path $BinaryPath -Label 'binary'
if ($binary.Name -ne $contract.target.binary_name) {
  Throw-BuildError "expected canonical $($contract.target.binary_name), got $($binary.Name)"
}
if ($binary.Length -le 0 -or $binary.Length -gt 268435456) {
  Throw-BuildError "binary size is outside the allowed range: $($binary.Length)"
}

$versionMatch = [regex]::Match(
  $Version,
  '^v?(?<major>0|[1-9][0-9]*)\.(?<minor>0|[1-9][0-9]*)\.(?<patch>0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$'
)
if (-not $versionMatch.Success) {
  Throw-BuildError "invalid release version $Version"
}
$parts = @(
  [int64]$versionMatch.Groups['major'].Value,
  [int64]$versionMatch.Groups['minor'].Value,
  [int64]$versionMatch.Groups['patch'].Value
)
foreach ($part in $parts) {
  if ($part -gt 65535) {
    Throw-BuildError "release version component exceeds 65535: $Version"
  }
}
$setupVersion = '{0}.{1}.{2}.0' -f $parts[0], $parts[1], $parts[2]

$outputRoot = [IO.Path]::GetFullPath($OutputDirectory)
if (-not (Test-Path -LiteralPath $outputRoot)) {
  New-Item -ItemType Directory -Path $outputRoot -Force | Out-Null
}
$outputRootItem = Get-Item -LiteralPath $outputRoot -Force
if (-not $outputRootItem.PSIsContainer) {
  Throw-BuildError "output directory is not a directory: $outputRoot"
}
if (($outputRootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
  Throw-BuildError "output directory must not be a symlink or reparse point: $outputRoot"
}

$artifactName = [string]$contract.target.artifact_name
$digestName = [string]$contract.handoff.payload_digest_filename
$outputPath = Join-Path $outputRoot $artifactName
$digestPath = Join-Path $outputRoot $digestName
foreach ($reservedPath in @($outputPath, $digestPath)) {
  if (Test-Path -LiteralPath $reservedPath) {
    Throw-BuildError "refusing to overwrite existing output: $reservedPath"
  }
}

$scriptPath = Join-Path $repoRoot ([string]$contract.script.path)
$script = Assert-RegularFile -Path $scriptPath -Label 'Inno Setup script'
$compiler = Resolve-Compiler -RequestedPath $CompilerPath -ExpectedName ([string]$contract.toolchain.compiler)
$compilerVersion = (& $compiler '--version' 2>&1 | Out-String).Trim()
if ($LASTEXITCODE -ne 0 -or $compilerVersion -notmatch [regex]::Escape([string]$contract.toolchain.version)) {
  Throw-BuildError "compiler version does not match pinned Inno Setup $($contract.toolchain.version): $compilerVersion"
}

$tempRoot = Join-Path $outputRoot ('.codemcp-windows-setup-' + [guid]::NewGuid().ToString('N'))
$outputBaseFilename = [IO.Path]::GetFileNameWithoutExtension($artifactName)
New-Item -ItemType Directory -Path $tempRoot | Out-Null

try {
  $arguments = @(
    '--quiet-progress',
    '--no-ide-signtools',
    '--no-signing',
    ('--define={0}={1}' -f $contract.script.defines.binary_path, $binary.FullName),
    ('--define={0}={1}' -f $contract.script.defines.setup_version, $setupVersion),
    ('--define={0}={1}' -f $contract.script.defines.output_dir, $tempRoot),
    ('--define={0}={1}' -f $contract.script.defines.output_base_filename, $outputBaseFilename),
    ('--define={0}={1}' -f $contract.script.defines.target_arch, $TargetArch),
    $script.FullName
  )

  & $compiler @arguments
  if ($LASTEXITCODE -ne 0) {
    Throw-BuildError "ISCC.exe failed with exit code $LASTEXITCODE"
  }

  $stagedPath = Join-Path $tempRoot $artifactName
  $staged = Assert-RegularFile -Path $stagedPath -Label 'generated setup'
  if ($staged.Length -le 0 -or $staged.Length -gt 536870912) {
    Throw-BuildError "generated setup size is outside the allowed range: $($staged.Length)"
  }

  $payloadHash = (Get-FileHash -LiteralPath $binary.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
  $stagedDigest = Join-Path $tempRoot $digestName
  $utf8NoBom = [Text.UTF8Encoding]::new($false)
  [IO.File]::WriteAllText($stagedDigest, ($payloadHash + '  cm.exe' + "`n"), $utf8NoBom)

  Move-Item -LiteralPath $staged.FullName -Destination $outputPath
  Move-Item -LiteralPath $stagedDigest -Destination $digestPath
  Write-Output $outputPath
}
catch {
  Remove-Item -LiteralPath $outputPath -Force -ErrorAction SilentlyContinue
  Remove-Item -LiteralPath $digestPath -Force -ErrorAction SilentlyContinue
  throw
}
finally {
  Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
}
