@echo off
rem Double-click to remove station-xiaozhi. Optional argument: Station IP address.
if "%~1"=="" (
    powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" -Action uninstall
) else (
    powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1" -Station %1 -Action uninstall
)
pause
