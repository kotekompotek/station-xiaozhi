@echo off
rem Double-click to install station-xiaozhi. Optional argument: Station IP address.
if "%~1"=="" (
    powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1"
) else (
    powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" -Station %1
)
pause
