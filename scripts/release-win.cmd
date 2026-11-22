@echo off
REM Windows release entry (Bypass ExecutionPolicy -> release.ps1)
REM From repo root:
REM   scripts\release-win.cmd
REM   scripts\release-win.cmd 0.2.0
REM   scripts\release-win.cmd 0.2.0 -DryRun

setlocal
cd /d "%~dp0.."
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0release.ps1" %*
set EXITCODE=%ERRORLEVEL%
endlocal & exit /b %EXITCODE%
