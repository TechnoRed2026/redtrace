@echo off
setlocal EnableExtensions
title RedTrace Installer
cd /d "%~dp0"

echo.
echo  ===== RedTrace installer =====
echo.

where go >nul 2>nul
if errorlevel 1 goto :nogo

set "INSTALL_DIR=%LOCALAPPDATA%\RedTrace"
if not exist "%INSTALL_DIR%" mkdir "%INSTALL_DIR%"

echo [*] Building redtrace.exe ...
set CGO_ENABLED=0
go build -trimpath -ldflags "-s -w" -o "%INSTALL_DIR%\redtrace.exe" .
if errorlevel 1 goto :buildfail

echo [*] Installed to %INSTALL_DIR%
set "RT_DIR=%INSTALL_DIR%"
powershell -NoProfile -ExecutionPolicy Bypass -Command "$d=$env:RT_DIR; $p=[Environment]::GetEnvironmentVariable('Path','User'); if([string]::IsNullOrEmpty($p)){$p=''}; $parts=@($p.Split(';') | Where-Object { $_ -ne '' }); if($parts -notcontains $d){ $parts += $d; [Environment]::SetEnvironmentVariable('Path',($parts -join ';'),'User'); Write-Host '[*] Added to user PATH' } else { Write-Host '[*] Already in user PATH' }"
if errorlevel 1 goto :pathfail

set "PATH=%PATH%;%INSTALL_DIR%"
echo.
echo [OK] Done. Open a NEW terminal and run:  redtrace
echo      Example:  redtrace mc.hypixel.net -s minecraft
echo      Note: Windows asks for Administrator rights ^(UAC^) because raw ICMP sockets need it.
echo.
pause
exit /b 0

:nogo
echo [!] Go is not installed or not in PATH.
echo     Install it from https://go.dev/dl/   or run:  winget install GoLang.Go
echo     Then open a NEW terminal and run install.bat again.
pause
exit /b 1

:buildfail
echo [!] Build failed.
pause
exit /b 1

:pathfail
echo [!] Could not update PATH. Add this folder to PATH manually: %INSTALL_DIR%
pause
exit /b 1
