@echo off
REM ============================================================
REM  iCode Build Script — builds CLI + Desktop in one command
REM  Usage: build.bat
REM  Outputs: icode.exe (CLI), desktop/release/ (installer)
REM ============================================================
setlocal enabledelayedexpansion

set "ROOT=%~dp0"
cd /d "%ROOT%"

echo.
echo   ======================================
echo   iCode Build Script
echo   ======================================
echo.

REM ── 1. Go CLI ──────────────────────────────────────
echo   [1/3] Building CLI (icode.exe)...
go build -ldflags="-s -w" -o icode.exe . 2>nul
if errorlevel 1 (
    echo   ERROR: Go build failed. Is Go installed?
    exit /b 1
)
echo   Done. (%icfile% bytes)
for %%A in (icode.exe) do echo   Size: %%~zA bytes

REM ── 2. Desktop Frontend ────────────────────────────
echo.
echo   [2/3] Building Desktop UI...
cd /d "%ROOT%desktop"

if not exist "node_modules" (
    echo   Installing dependencies...
    call npm install --no-audit --no-fund 2>nul
    if errorlevel 1 (
        echo   WARNING: npm install failed. Try running manually:
        echo   cd desktop ^&^& npm install
    )
)

echo   Building Vite frontend...
call npx vite build 2>nul
if errorlevel 1 (
    echo   WARNING: Vite build failed. Skipping desktop package.
    goto :done
)
echo   Frontend built.

REM ── 3. Desktop Package ─────────────────────────────
echo.
echo   [3/3] Packaging Desktop (this may take a while)...
call npx electron-builder --win portable 2>nul
if errorlevel 1 (
    echo   Running fallback: npx electron-builder --win
    call npx electron-builder --win 2>nul
)
echo   Desktop package built — see desktop\release\

:done
echo.
echo   ======================================
echo   Build complete.
echo   CLI:  .\icode.exe
echo   Desk: desktop\release\iCode*.exe
echo   ======================================
echo.
pause
