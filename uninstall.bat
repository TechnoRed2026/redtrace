@echo off
setlocal EnableExtensions
set "INSTALL_DIR=%LOCALAPPDATA%\RedTrace"
set "RT_DIR=%INSTALL_DIR%"
powershell -NoProfile -ExecutionPolicy Bypass -Command "$d=$env:RT_DIR; $p=[Environment]::GetEnvironmentVariable('Path','User'); if($p){ $parts=@($p.Split(';') | Where-Object { $_ -ne '' -and $_ -ne $d }); [Environment]::SetEnvironmentVariable('Path',($parts -join ';'),'User') }"
if exist "%INSTALL_DIR%" rmdir /s /q "%INSTALL_DIR%"
echo [OK] RedTrace removed.
pause
