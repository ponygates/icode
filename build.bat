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
REM      → 双击 icode.exe    启动增强 TUI（加强版 CLI，带滚动条/鼠标/快捷键）
REM      → 桌面版请显式用 icode desktop 或 icode-desktop.exe
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
    REM Call the local vite directly instead of "npx": on this machine npx
    REM resolves to a pnpm shim (pnpm dlx) whose flags/behaviour differ from
    REM npm's npx. node_modules\.bin\vite.cmd is what npm install produced,
    REM so it always matches the installed deps. No 2^>nul either — build
    REM errors must be visible, not silently replaced by "stale frontend".
    set "FRONTEND_OK=1"
    if exist "node_modules\.bin\vite.cmd" (
        call "node_modules\.bin\vite.cmd" build
    ) else (
        echo   ERROR: node_modules\.bin\vite.cmd not found - did npm install fail?
        set "FRONTEND_OK=0"
    )
    if errorlevel 1 set "FRONTEND_OK=0"
    REM NOTE: must use delayed expansion (!FRONTEND_OK!) here — this whole
    REM block lives inside the outer "if BUILD_DESKTOP" parenthesized block,
    REM and cmd expands %VAR% for the ENTIRE block at parse time, so
    REM %FRONTEND_OK% would expand to its pre-block value (empty) and this
   REM test could never see the vite result. !VAR! evaluates at run time.
    if "!FRONTEND_OK!"=="1" (
        echo   Frontend built.
        REM Copy frontend dist for Go embed.
        REM  /MIR (mirror), NOT /E: Vite names its bundles by content hash, so a
        REM  plain copy leaves every previous build's chunks behind in the
        REM  destination and go:embed then bakes all of them into the binary
        REM  (24 stale files / ~1.4 MB per executable, measured 2026-09-01).
        REM  /MIR purges files in the destination that no longer exist here.
        if exist "dist" (
            robocopy "dist" "%ROOT%internal\embedded\dist" /MIR /NFL /NDL /NJH /NJS /NP >nul
            echo   Frontend mirrored to embedded ^(stale chunks purged^).
        )
    ) else (
        echo   WARNING: Vite build failed. Building with -tags noembedded:
        echo   this binary will have NO embedded desktop UI until the
        echo   frontend builds again ^(icode desktop will not work^).
    )
    cd /d "%ROOT%"
)

REM ── 2. Go Build ─────────────────────────────────
echo.
echo   [2/3] Building icode.exe (single binary)...

REM Determine build flags
REM Console subsystem (NOT -H windowsgui): a GUI-subsystem binary breaks the
REM console IME bridge on Windows — the conhost/ConPTY input path treats GUI
REM clients differently, so Chinese IME composition leaks raw pinyin letters
REM into the app AND the IME commit string gets echoed straight into the
REM screen buffer (the "text and garbage appear above the input box" bug,
REM confirmed via ~/.icode/screen-dump.txt + cli.log). A console-subsystem
REM exe double-clicked in Explorer simply opens its own terminal window and
REM runs the TUI there — which is the desired behaviour anyway.
set "LDFLAGS=-s -w"
set "BUILD_TAGS="
REM noembedded used to be a dead tag: embed.go had no build guard, so a
REM "CLI-only" build silently embedded the frontend anyway. embed.go is now
REM guarded by //go:build !noembedded (nil-Frontend stub in embed_stub.go),
REM and the tag is also applied when a desktop build was requested but the
REM frontend failed — never bake a stale UI into a "fresh" binary.
if "%BUILD_DESKTOP%"=="0" set "BUILD_TAGS=-tags noembedded"
if "%BUILD_DESKTOP%"=="1" if not "%FRONTEND_OK%"=="1" set "BUILD_TAGS=-tags noembedded"

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
echo     双击 icode.exe          — 启动增强 TUI（加强版 CLI）
echo.
echo   之前的 desktop-launcher.exe 已废弃
echo   （桌面功能已合并到 icode.exe 中）
echo   ======================================
echo.
pause
