@echo off
REM ============================================================
REM  iCode Installer — one-command setup for Windows
REM  Usage: install.bat
REM  After running, restart your terminal and type 'icode'.
REM ============================================================
setlocal enabledelayedexpansion

set "ICO_DIR=%~dp0"
set "ICO_DIR=%ICO_DIR:~0,-1%"

echo.
echo   iCode Installer
echo   ==============
echo   Directory: %ICO_DIR%
echo.

REM Step 1: Build if needed
if not exist "%ICO_DIR%\icode.exe" (
    echo   [1/3] Building icode.exe...
    cd /d "%ICO_DIR%"
    go build -ldflags="-s -w -H windowsgui" -o icode.exe . 2>nul
    if errorlevel 1 (
        echo   ERROR: Build failed. Is Go installed? Run 'go version'.
        pause
        exit /b 1
    )
    echo   Done.
) else (
    echo   [1/3] icode.exe found.
)

REM Step 2: Add to PATH (current user, permanent)
echo   [2/3] Adding to PATH...
set "USER_PATH="
for /f "tokens=2*" %%a in ('reg query HKCU\Environment /v Path 2^>nul ^| find "REG_"') do set "USER_PATH=%%b"
if defined USER_PATH (
    echo !USER_PATH! | find /i "%ICO_DIR%" >nul
    if errorlevel 1 (
        setx Path "!USER_PATH!;%ICO_DIR%" >nul
        echo   Added to user PATH.
    ) else (
        echo   Already in PATH.
    )
) else (
    setx Path "%ICO_DIR%" >nul
    echo   Created user PATH.
)

REM Step 3: Test
echo   [3/3] Verifying...
"%ICO_DIR%\icode.exe" doctor 2>&1 | findstr "Providers" >nul
if errorlevel 1 (
    echo   WARNING: icode doctor check failed
) else (
    echo   iCode is ready.
)

echo.
echo   ========================================
echo   RESTART your terminal, then type: icode
echo   ========================================
echo.
pause
