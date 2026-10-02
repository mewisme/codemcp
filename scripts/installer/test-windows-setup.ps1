$ErrorActionPreference = 'Stop'
if (-not $IsWindows -and $env:OS -ne 'Windows_NT') {
  throw 'Windows setup smoke must run on Windows.'
}

$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$makensisCommand = Get-Command makensis.exe -ErrorAction SilentlyContinue
$makensisPath = if ($makensisCommand) { $makensisCommand.Source } else { $null }
if (-not $makensisPath) {
  $candidate = Join-Path ${env:ProgramFiles(x86)} 'NSIS\makensis.exe'
  if (Test-Path -LiteralPath $candidate -PathType Leaf) {
    $makensisPath = $candidate
  }
}
if (-not $makensisPath) { throw 'makensis.exe is required for Windows setup smoke.' }

$tempRoot = if ($env:RUNNER_TEMP) { $env:RUNNER_TEMP } else { $env:TEMP }
if (-not $tempRoot) { throw 'temporary directory is unavailable.' }
$tmp = Join-Path $tempRoot ("cm-nsis-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
$oldInstallDir = $env:CM_INSTALL_DIR
$oldConfigDir = $env:CM_CONFIG_DIR
$oldTelemetry = $env:CM_TELEMETRY
try {
  $binary = Join-Path $tmp 'cm.exe'
  $setup = Join-Path $tmp 'codemcp_windows_amd64_setup.exe'
  $ldflags = '-s -w -X go.mewis.me/codemcp/internal/version.ReleaseTag=v9.9.9'
  & go build -trimpath -ldflags $ldflags -o $binary .
  if ($LASTEXITCODE -ne 0) { throw "fixture build failed with exit code $LASTEXITCODE" }

  $template = Join-Path $repoRoot 'installer\windows\codemcp.nsi'
  & $makensisPath '-V2' "-DBINARY_PATH=$binary" "-DOUTPUT_PATH=$setup" '-DSETUP_ARCH=amd64' $template
  if ($LASTEXITCODE -ne 0) { throw "makensis failed with exit code $LASTEXITCODE" }
  if (-not (Test-Path -LiteralPath $setup -PathType Leaf)) { throw 'setup executable was not generated' }

  $env:CM_INSTALL_DIR = Join-Path $tmp 'managed'
  $env:CM_CONFIG_DIR = Join-Path $tmp 'config'
  $env:CM_TELEMETRY = '0'
  $setupProcess = Start-Process -FilePath $setup -ArgumentList '/S' -Wait -PassThru
  if ($setupProcess.ExitCode -ne 0) { throw "setup smoke failed with exit code $($setupProcess.ExitCode)" }

  $installed = Join-Path $env:CM_INSTALL_DIR 'current\cm.exe'
  if (-not (Test-Path -LiteralPath $installed -PathType Leaf)) {
    throw "managed current binary missing at $installed"
  }
  $version = & $installed --version
  if ($LASTEXITCODE -ne 0 -or ($version -join [Environment]::NewLine) -notmatch 'cm version 9\.9\.9') {
    throw "installed binary version mismatch: $($version -join ' ')"
  }
} finally {
  $env:CM_INSTALL_DIR = $oldInstallDir
  $env:CM_CONFIG_DIR = $oldConfigDir
  $env:CM_TELEMETRY = $oldTelemetry
  if (Test-Path -LiteralPath $tmp) {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }
}

Write-Host 'Windows NSIS setup smoke OK'
