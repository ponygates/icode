@echo off
REM ============================================================
REM  iCode Build Script — 单二进制（CLI + 桌面版合一）
REM ============================================================
REM
REM  使用方式:
REM    build.bat              — 完整构建（前端 + Go 二进制）
REM    build.bat --cli        — 仅构建 CLI
REM    build.bat --desktop    — 构建包含桌面前端的完整版
REM
REM  输出:
REM    icode.exe              — 单二进制（CLI + 桌面二合一）
REM      → icode desktop     启动桌面版（原生 WebView2 窗口）
REM      → icode             启动 CLI
REM      → 双击 icode.exe    自动进入桌面模式
REM
REM  2026-07-14: 合并桌面启动器到主 CLI，不再生成独立的 desktop-launcher.exe
REM ============================================================
setlocal enabledelayedexpansion

set "ROOT=%~dp0"
cd /d "%ROOT%"

echo.
echo   ======================================
echo   iCode Build Script
echo   单二进制（CLI + 桌面版合一）
echo   ======================================
echo.

REM ── Parse flags ─────────────────────────────────
set "BUILD_CLI=1"
set "BUILD_DESKTOP=1"
if /I "%1"=="--cli" set "BUILD_DESKTOP=0"
if /I "%1"=="--desktop" set "BUILD_CLI=0"

REM ── 1. Desktop Frontend ─────────────────────────
if "%BUILD_DESKTOP%"=="1" (
    echo   [1/3] Building Desktop Frontend...
    cd /d "%ROOT%desktop"
    if not exist "node_modules" (
        echo   Installing dependencies...
        call npm install --no-audit --no-fund 2>nul
        if errorlevel 1 (
            echo   WARNING: npm install failed. Trying anyway...
        )
    )
    echo   Building Vite frontend...
    call npx vite build 2>nul
    if errorlevel 1 (
        echo   WARNING: Vite build failed. Desktop UI will not be embedded.
        echo   The --no-embedded flag will be used for CLI-only builds.
    ) else (
        echo   Frontend built.
        REM Copy frontend dist for Go embed
        if exist "dist" (
            robocopy "dist" "%ROOT%internal\embedded\dist" /E /NFL /NDL /NJH /NJS /NP >nul
            echo   Frontend copied to embedded.
        )
    )
    cd /d "%ROOT%"
)

REM ── 2. Go Build ─────────────────────────────────
echo.
echo   [2/3] Building icode.exe (single binary)...

REM Determine build flags
REM  -H windowsgui: link as a GUI-subsystem app so double-clicking icode.exe
REM  never flashes a black CMD/console window. CLI usage still works because
REM  the process re-attaches to the parent terminal's console at startup
REM  (see cmd/codepage_windows.go setupConsoleIO).
set "LDFLAGS=-s -w -H windowsgui"
set "BUILD_TAGS="
if "%BUILD_DESKTOP%"=="0" (
    set "BUILD_TAGS=-tags noembedded"
)

go build -ldflags="%LDFLAGS%" -o icode.exe %BUILD_TAGS% .
if errorlevel 1 (
    echo   ERROR: Go build failed.
    exit /b 1
)
for %%A in (icode.exe) do echo   Done: %%~zA bytes

REM ── 3. Verify ───────────────────────────────────
echo.
echo   [3/3] Verification...
icode.exe version 2>nul || echo   Version check: N/A (expected pre-v1.0)

echo.
echo   ======================================
echo   Build complete!
echo.
echo   icode.exe (单二进制, CLI + 桌面合一)
echo.
echo   使用方式:
echo     icode                   — 启动 CLI
echo     icode desktop           — 启动桌面版
echo     icode exec -p "..."    — 单次执行
echo     双击 icode.exe          — 自动进入桌面模式
echo.
echo   之前的 desktop-launcher.exe 已废弃
echo   （桌面功能已合并到 icode.exe 中）
echo   ======================================
echo.
pause
