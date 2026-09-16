@echo off
rem ---------------------------------------------------------------------------
rem  Git AI Tool - pack-zip launcher
rem  Thin wrapper so "packZip" can be typed directly in cmd / PowerShell.
rem  All real logic lives in packZip.ps1 next to this file.
rem ---------------------------------------------------------------------------
setlocal

set "PS1=%~dp0packZip.ps1"

if not exist "%PS1%" (
  echo [ERROR] packZip.ps1 not found in "%~dp0"
  exit /b 1
)

powershell -NoProfile -ExecutionPolicy Bypass -File "%PS1%" %*
set "RC=%ERRORLEVEL%"

if not "%RC%"=="0" (
  echo.
  echo [FAILED] pack-zip exited with code %RC%
)

exit /b %RC%
