# iCode PowerShell launcher
# Usage: .\icode.ps1 chat
# Or add this directory to PATH for global access.

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$binary = Join-Path $scriptDir "icode.exe"

if (-not (Test-Path $binary)) {
    Write-Host "iCode binary not found. Run 'go build -o icode.exe .' first." -ForegroundColor Red
    exit 1
}

& $binary $args
exit $LASTEXITCODE
