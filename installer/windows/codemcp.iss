#ifndef BinaryPath
  #error BinaryPath is required
#endif
#ifndef SetupVersion
  #error SetupVersion is required
#endif
#ifndef OutputDir
  #error OutputDir is required
#endif
#ifndef OutputBaseFilename
  #error OutputBaseFilename is required
#endif
#ifndef TargetArch
  #error TargetArch is required
#endif
#if TargetArch != "amd64"
  #error TargetArch must be amd64
#endif

[Setup]
AppId=CodeMCPBootstrap
AppName=CodeMCP
AppVersion={#SetupVersion}
AppPublisher=mewisme
AppComments=CodeMCP is a workspace-aware execution, context, and agent orchestration server for AI clients.
VersionInfoCompany=mewisme
VersionInfoDescription=CodeMCP is a workspace-aware execution, context, and agent orchestration server for AI clients.
VersionInfoProductName=CodeMCP
VersionInfoVersion={#SetupVersion}
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=
RedirectionGuard=no
SetupArchitecture=x64
ArchitecturesAllowed=x64compatible
CreateAppDir=no
DefaultDirName={tmp}\CodeMCP
DisableDirPage=yes
DisableProgramGroupPage=yes
UsePreviousAppDir=no
Uninstallable=no
CreateUninstallRegKey=no
ChangesEnvironment=yes
Compression=lzma2
SolidCompression=yes
OutputDir={#OutputDir}
OutputBaseFilename={#OutputBaseFilename}
SetupLogging=no

[Files]
Source: "{#BinaryPath}"; DestName: "cm.exe"; Flags: dontcopy noencryption

[Code]
var
  DelegatedExitCode: Integer;
  DelegatedInstallRan: Boolean;

function NormalizedPath(const Value: String): String;
begin
  Result := RemoveBackslashUnlessRoot(Trim(Value));
end;

function IsDefaultInstallRoot(const InstallRoot, DefaultRoot: String): Boolean;
begin
  Result := SameText(NormalizedPath(InstallRoot), NormalizedPath(DefaultRoot));
end;

function UserPathContains(const PathValue, Entry: String): Boolean;
var
  Haystack: String;
  Needle: String;
begin
  Haystack := ';' + Lowercase(PathValue) + ';';
  Needle := ';' + Lowercase(Entry) + ';';
  Result := Pos(Needle, Haystack) > 0;
end;

procedure EnsureUserPath(const CurrentDir: String);
var
  ExistingPath: String;
  NewPath: String;
begin
  ExistingPath := '';
  RegQueryStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', ExistingPath);
  if UserPathContains(ExistingPath, CurrentDir) then
    Exit;

  if ExistingPath = '' then
    NewPath := CurrentDir
  else
    NewPath := CurrentDir + ';' + ExistingPath;

  if not RegWriteExpandStringValue(HKEY_CURRENT_USER, 'Environment', 'Path', NewPath) then
    Log('CodeMCP setup could not update the current-user PATH.');
end;

procedure RunCanonicalInstall;
var
  BinaryPath: String;
  InstallRoot: String;
  DefaultRoot: String;
  CurrentDir: String;
  ExecResult: Integer;
begin
  DelegatedInstallRan := True;
  DelegatedExitCode := 0;
  ExtractTemporaryFile('cm.exe');
  BinaryPath := ExpandConstant('{tmp}\cm.exe');

  if not ExecAndLogOutput(BinaryPath, 'install', '', SW_SHOWNORMAL, ewWaitUntilTerminated, ExecResult, nil) then
  begin
    DelegatedExitCode := ExecResult;
    if DelegatedExitCode = 0 then
      DelegatedExitCode := 1;
    Log('CodeMCP install could not start; error code ' + IntToStr(DelegatedExitCode) + '.');
    Exit;
  end;

  DelegatedExitCode := ExecResult;
  if DelegatedExitCode <> 0 then
  begin
    Log('CodeMCP install failed with exit code ' + IntToStr(DelegatedExitCode) + '.');
    Exit;
  end;

  DefaultRoot := AddBackslash(GetEnv('USERPROFILE')) + '.cm';
  InstallRoot := GetEnv('CM_INSTALL_DIR');
  if InstallRoot = '' then
    InstallRoot := DefaultRoot;

  if IsDefaultInstallRoot(InstallRoot, DefaultRoot) then
  begin
    CurrentDir := AddBackslash(NormalizedPath(DefaultRoot)) + 'current';
    EnsureUserPath(CurrentDir);
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssInstall then
    RunCanonicalInstall;
end;

function GetCustomSetupExitCode: Integer;
begin
  if DelegatedInstallRan and (DelegatedExitCode <> 0) then
    Result := DelegatedExitCode
  else
    Result := 0;
end;
