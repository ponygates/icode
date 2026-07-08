# iCode Setup — Add to PATH
# Run this once to make 'icode' available globally.

$icoDir = Split-Path -Parent $MyInvocation.MyCommand.Path

Write-Host "iCode directory: $icoDir" -ForegroundColor Cyan

# Check if already in PATH
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -like "*$icoDir*") {
    Write-Host "Already in PATH. You can type 'icode' from anywhere." -ForegroundColor Green
} else {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$icoDir", "User")
    Write-Host "Added to PATH! Restart PowerShell and type 'icode'." -ForegroundColor Green
}

# Also create a local alias for the current session
Set-Alias -Name icode -Value "$icoDir\icode.exe" -Scope Global
Write-Host "Alias 'icode' set for this session. Try: icode doctor" -ForegroundColor Green
