Unicode true
RequestExecutionLevel user

!ifndef BINARY_PATH
  !error "BINARY_PATH is required"
!endif
!ifndef OUTPUT_PATH
  !error "OUTPUT_PATH is required"
!endif
!ifndef SETUP_ARCH
  !error "SETUP_ARCH is required"
!endif
!ifndef SETUP_VERSION
  !define SETUP_VERSION "0.0.0.0"
!endif

!include "LogicLib.nsh"
!include "StrFunc.nsh"
!include "WinMessages.nsh"
${StrStr}

Name "CodeMCP"
Caption "CodeMCP Setup (${SETUP_ARCH})"
BrandingText "CodeMCP"
OutFile "${OUTPUT_PATH}"
VIProductVersion "${SETUP_VERSION}"
VIAddVersionKey /LANG=1033 "ProductName" "CodeMCP"
VIAddVersionKey /LANG=1033 "FileDescription" "A secure, workspace-bound MCP bridge connecting ChatGPT, Claude, and other AI agents to your machine."
VIAddVersionKey /LANG=1033 "FileVersion" "${SETUP_VERSION}"
VIAddVersionKey /LANG=1033 "CompanyName" "mewisme"
SetCompressor /SOLID lzma
ShowInstDetails show
AutoCloseWindow true
Page instfiles

Var InstallRoot
Var CurrentDir

Section "CodeMCP"
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File /oname=cm.exe "${BINARY_PATH}"

  ReadEnvStr $InstallRoot "CM_INSTALL_DIR"
  ${If} $InstallRoot == ""
    StrCpy $InstallRoot "$PROFILE\.cm"
  ${EndIf}
  StrCpy $CurrentDir "$InstallRoot\current"

  ; Make the selected root explicit for the delegated managed installer rather
  ; than relying on launcher-specific environment inheritance.
  System::Call 'kernel32::SetEnvironmentVariable(t, t) i("CM_INSTALL_DIR", "$InstallRoot").r1'
  ${If} $1 == 0
    DetailPrint "Could not prepare the managed install root."
    SetErrorLevel 1
    Quit
  ${EndIf}

  DetailPrint "Installing CodeMCP through the canonical managed installer..."
  ExecWait '"$PLUGINSDIR\cm.exe" install' $0
  ${If} $0 != 0
    DetailPrint "CodeMCP install failed with exit code $0."
    SetErrorLevel $0
    Quit
  ${EndIf}

  ; Match the PowerShell bootstrap: persist PATH only for the default managed
  ; user layout. Custom CM_INSTALL_DIR callers retain explicit PATH ownership.
  StrCmp $InstallRoot "$PROFILE\.cm" 0 setup_done
  Call EnsureUserPath

setup_done:
SectionEnd

Function EnsureUserPath
  ReadRegStr $0 HKCU "Environment" "Path"
  StrCpy $1 ";$0;"
  StrCpy $2 ";$CurrentDir;"
  ${StrStr} $3 "$1" "$2"
  StrCmp $3 "" path_add path_done

path_add:
  ClearErrors
  StrCmp $0 "" path_empty
  WriteRegExpandStr HKCU "Environment" "Path" "$CurrentDir;$0"
  Goto path_notify

path_empty:
  WriteRegExpandStr HKCU "Environment" "Path" "$CurrentDir"

path_notify:
  IfErrors path_done
  SendMessage ${HWND_BROADCAST} ${WM_SETTINGCHANGE} 0 "STR:Environment" /TIMEOUT=5000

path_done:
FunctionEnd
