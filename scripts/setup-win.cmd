@echo off
REM Clean Windows: install release deps (Bypass ExecutionPolicy)
REM Run as Administrator:
REM   scripts\setup-win.cmd

setlocal
cd /d "%~dp0.."
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0setup-win.ps1" %*
set EXITCODE=%ERRORLEVEL%
endlocal & exit /b %EXITCODE%
