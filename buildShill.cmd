@echo off
rem ---------------------------------------------------------------------------
rem  Git AI Tool - build launcher
rem  Thin wrapper so "buildShill" can be typed directly in cmd / PowerShell.
rem  All real logic lives in buildShill.ps1 next to this file.
rem ---------------------------------------------------------------------------
setlocal

set "PS1=%~dp0buildShill.ps1"

if not exist "%PS1%" (
  echo [ERROR] buildShill.ps1 not found in "%~dp0"
  exit /b 1
)

powershell -NoProfile -ExecutionPolicy Bypass -File "%PS1%" %*
set "RC=%ERRORLEVEL%"

if not "%RC%"=="0" (
  echo.
  echo [FAILED] build exited with code %RC%
)

exit /b %RC%
