Unicode true
RequestExecutionLevel user
SetCompressor /SOLID lzma

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"
!include "nsDialogs.nsh"
!include "WinMessages.nsh"

!ifndef PAYLOAD_DIR
  !error "PAYLOAD_DIR is required"
!endif
!ifndef OUT_FILE
  !error "OUT_FILE is required"
!endif
!ifndef APP_VERSION
  !define APP_VERSION "0.1.0"
!endif
!ifndef ICON_FILE
  !error "ICON_FILE is required"
!endif

!define APP_NAME "证书管家"
!define APP_EXE "CertKeeper.exe"
!define PROCESS_BASENAME "CertKeeper"
!define INSTALLER_QUIT_ARG "--quit-for-install"
!define INSTALL_DIR_DEFAULT "$LOCALAPPDATA\CertKeeper"
!define UNINSTALL_REG_PATH "Software\Microsoft\Windows\CurrentVersion\Uninstall\CertKeeperDesktop"
!define RUN_REG_PATH "Software\Microsoft\Windows\CurrentVersion\Run"
!define RUN_VALUE_NAME "CertKeeperDesktop"
!define PUBLISHER_NAME "Cloud Relay"

Name "${APP_NAME}"
OutFile "${OUT_FILE}"
InstallDir "${INSTALL_DIR_DEFAULT}"
InstallDirRegKey HKCU "${UNINSTALL_REG_PATH}" "InstallLocation"
Icon "${ICON_FILE}"
UninstallIcon "${ICON_FILE}"
ShowInstDetails show
ShowUninstDetails show
BrandingText "Cloud Relay"
XPStyle on

Var ExistingInstallDir
Var RunningProcessPath
Var PromptedOverwriteDir
Var FinishDesktopShortcutCheckbox
Var FinishStartMenuShortcutCheckbox
Var FinishAutoStartCheckbox
Var FinishLaunchCheckbox

!define MUI_ABORTWARNING
!define MUI_ICON "${ICON_FILE}"
!define MUI_UNICON "${ICON_FILE}"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
Page custom FinishPageCreate FinishPageLeave

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "SimpChinese"

Function .onInit
  StrCpy $PromptedOverwriteDir ""
  Call DetectExistingInstallDir
  ${If} $ExistingInstallDir != ""
    StrCpy $INSTDIR $ExistingInstallDir
    MessageBox MB_OKCANCEL|MB_ICONQUESTION "已检测到 ${APP_NAME} 安装目录：$\r$\n$ExistingInstallDir$\r$\n$\r$\n点击“确定”继续覆盖安装，点击“取消”退出安装。" IDOK +2
    Abort
    StrCpy $PromptedOverwriteDir $ExistingInstallDir
  ${Else}
    StrCpy $INSTDIR "${INSTALL_DIR_DEFAULT}"
  ${EndIf}
  Call EnsureAppStopped
FunctionEnd

Function DetectExistingInstallDir
  StrCpy $ExistingInstallDir ""
  ReadRegStr $0 HKCU "${UNINSTALL_REG_PATH}" "InstallLocation"
  ${If} $0 != ""
  ${AndIf} ${FileExists} "$0\${APP_EXE}"
    StrCpy $ExistingInstallDir $0
  ${EndIf}
FunctionEnd

Function GetRunningProcessPath
  StrCpy $RunningProcessPath ""
  nsExec::ExecToStack 'powershell -NoProfile -ExecutionPolicy Bypass -Command "$p = Get-Process -Name ''${PROCESS_BASENAME}'' -ErrorAction SilentlyContinue | Select-Object -First 1; if ($p) { if ($p.Path) { [Console]::Write($p.Path) } else { [Console]::Write(''__RUNNING__'') } }"'
  Pop $0
  Pop $1
  ${If} $0 == "error"
    Return
  ${EndIf}
  StrCpy $RunningProcessPath $1
FunctionEnd

Function RequestRunningAppQuit
  Call GetRunningProcessPath
  StrCpy $0 $RunningProcessPath
  ${If} $0 == ""
  ${OrIf} $0 == "__RUNNING__"
    ${If} ${FileExists} "$ExistingInstallDir\${APP_EXE}"
      StrCpy $0 "$ExistingInstallDir\${APP_EXE}"
    ${ElseIf} ${FileExists} "$INSTDIR\${APP_EXE}"
      StrCpy $0 "$INSTDIR\${APP_EXE}"
    ${Else}
      Return
    ${EndIf}
  ${EndIf}
  ExecWait '"$0" "${INSTALLER_QUIT_ARG}"'
  Sleep 500
FunctionEnd

Function WaitForAppStop
  StrCpy $0 0
  wait_loop:
    Call GetRunningProcessPath
    ${If} $RunningProcessPath == ""
      Return
    ${EndIf}
    IntOp $0 $0 + 1
    ${If} $0 >= 20
      Return
    ${EndIf}
    Sleep 500
    Goto wait_loop
FunctionEnd

Function EnsureAppStopped
  running_check:
    Call GetRunningProcessPath
    ${If} $RunningProcessPath == ""
      Return
    ${EndIf}
    MessageBox MB_OKCANCEL|MB_ICONEXCLAMATION "${APP_NAME} 当前正在运行。$\r$\n$\r$\n点击“确定”将自动退出并继续安装，点击“取消”退出安装。" IDOK try_quit IDCANCEL cancel_install

  try_quit:
    Call RequestRunningAppQuit
    Call WaitForAppStop
    Call GetRunningProcessPath
    ${If} $RunningProcessPath == ""
      Return
    ${EndIf}
    MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "尚未确认 ${APP_NAME} 已退出。请手动关闭后点击“重试”，或点击“取消”退出安装。" IDRETRY running_check IDCANCEL cancel_install

  cancel_install:
    Abort
FunctionEnd

Function ConfirmOverwriteTarget
  ${IfNot} ${FileExists} "$INSTDIR\${APP_EXE}"
    Return
  ${EndIf}
  ${If} $PromptedOverwriteDir == $INSTDIR
    Return
  ${EndIf}
  MessageBox MB_OKCANCEL|MB_ICONQUESTION "目标目录已存在 ${APP_NAME}：$\r$\n$INSTDIR$\r$\n$\r$\n点击“确定”继续覆盖安装，点击“取消”退出安装。" IDOK +2
  Abort
  StrCpy $PromptedOverwriteDir $INSTDIR
FunctionEnd

Section "Install"
  SetShellVarContext current
  Call EnsureAppStopped
  Call ConfirmOverwriteTarget

  CreateDirectory "$INSTDIR"
  CreateDirectory "$INSTDIR\logs"

  SetOutPath "$INSTDIR"
  File "${PAYLOAD_DIR}/CertKeeper.exe"
  File /nonfatal "${PAYLOAD_DIR}/WebView2Loader.dll"
  File /nonfatal "${PAYLOAD_DIR}/README.txt"

  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "${UNINSTALL_REG_PATH}" "DisplayName" "${APP_NAME}"
  WriteRegStr HKCU "${UNINSTALL_REG_PATH}" "DisplayVersion" "${APP_VERSION}"
  WriteRegStr HKCU "${UNINSTALL_REG_PATH}" "Publisher" "${PUBLISHER_NAME}"
  WriteRegStr HKCU "${UNINSTALL_REG_PATH}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_REG_PATH}" "DisplayIcon" "$INSTDIR\${APP_EXE}"
  WriteRegStr HKCU "${UNINSTALL_REG_PATH}" "UninstallString" "$\"$INSTDIR\Uninstall.exe$\""
  WriteRegDWORD HKCU "${UNINSTALL_REG_PATH}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_REG_PATH}" "NoRepair" 1
SectionEnd

Function CreateDesktopShortcut
  SetShellVarContext current
  CreateShortCut "$DESKTOP\${APP_NAME}.lnk" "$INSTDIR\${APP_EXE}"
FunctionEnd

Function DeleteDesktopShortcut
  SetShellVarContext current
  Delete "$DESKTOP\${APP_NAME}.lnk"
FunctionEnd

Function CreateStartMenuShortcuts
  SetShellVarContext current
  CreateDirectory "$SMPROGRAMS\${APP_NAME}"
  CreateShortCut "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk" "$INSTDIR\${APP_EXE}"
  CreateShortCut "$SMPROGRAMS\${APP_NAME}\卸载${APP_NAME}.lnk" "$INSTDIR\Uninstall.exe"
FunctionEnd

Function DeleteStartMenuShortcuts
  SetShellVarContext current
  Delete "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk"
  Delete "$SMPROGRAMS\${APP_NAME}\卸载${APP_NAME}.lnk"
  RMDir "$SMPROGRAMS\${APP_NAME}"
FunctionEnd

Function EnableAutoStart
  WriteRegStr HKCU "${RUN_REG_PATH}" "${RUN_VALUE_NAME}" "$\"$INSTDIR\${APP_EXE}$\""
FunctionEnd

Function DisableAutoStart
  DeleteRegValue HKCU "${RUN_REG_PATH}" "${RUN_VALUE_NAME}"
FunctionEnd

Function FinishPageCreate
  !insertmacro MUI_HEADER_TEXT "安装完成" "请选择安装后的附加操作"
  nsDialogs::Create 1018
  Pop $0
  ${If} $0 == error
    Abort
  ${EndIf}

  ${NSD_CreateLabel} 0 0 100% 20u "${APP_NAME} 已安装完成。请选择需要启用的选项："
  Pop $0

  ${NSD_CreateCheckbox} 0 28u 100% 12u "创建桌面快捷方式"
  Pop $FinishDesktopShortcutCheckbox

  ${NSD_CreateCheckbox} 0 46u 100% 12u "创建开始菜单快捷方式"
  Pop $FinishStartMenuShortcutCheckbox
  ${NSD_SetState} $FinishStartMenuShortcutCheckbox ${BST_CHECKED}

  ${NSD_CreateCheckbox} 0 64u 100% 12u "开机自动启动"
  Pop $FinishAutoStartCheckbox

  ${NSD_CreateCheckbox} 0 88u 100% 12u "立即启动 ${APP_NAME}"
  Pop $FinishLaunchCheckbox
  ${NSD_SetState} $FinishLaunchCheckbox ${BST_CHECKED}

  nsDialogs::Show
FunctionEnd

Function FinishPageLeave
  ${NSD_GetState} $FinishDesktopShortcutCheckbox $0
  ${If} $0 == ${BST_CHECKED}
    Call CreateDesktopShortcut
  ${Else}
    Call DeleteDesktopShortcut
  ${EndIf}

  ${NSD_GetState} $FinishStartMenuShortcutCheckbox $0
  ${If} $0 == ${BST_CHECKED}
    Call CreateStartMenuShortcuts
  ${Else}
    Call DeleteStartMenuShortcuts
  ${EndIf}

  ${NSD_GetState} $FinishAutoStartCheckbox $0
  ${If} $0 == ${BST_CHECKED}
    Call EnableAutoStart
  ${Else}
    Call DisableAutoStart
  ${EndIf}

  ${NSD_GetState} $FinishLaunchCheckbox $0
  ${If} $0 == ${BST_CHECKED}
    Exec '"$INSTDIR\${APP_EXE}"'
  ${EndIf}
FunctionEnd

Section "Uninstall"
  SetShellVarContext current
  Call DeleteDesktopShortcut
  Call DeleteStartMenuShortcuts
  Call DisableAutoStart
  DeleteRegKey HKCU "${UNINSTALL_REG_PATH}"

  Delete "$INSTDIR\Uninstall.exe"
  Delete "$INSTDIR\${APP_EXE}"
  Delete "$INSTDIR\WebView2Loader.dll"
  Delete "$INSTDIR\README.txt"
  RMDir /r "$INSTDIR\logs"
  RMDir "$INSTDIR"
SectionEnd
