$ErrorActionPreference = "Stop"

$RootDir = Split-Path -Parent $PSScriptRoot

Push-Location (Join-Path $RootDir "webui")
npm ci
npm run build
Pop-Location

$distPath = Join-Path $RootDir "internal/webui/dist"
if (Test-Path $distPath) {
    Remove-Item -LiteralPath $distPath -Recurse -Force
}

Copy-Item -Path (Join-Path $RootDir "webui/dist") -Destination $distPath -Recurse

Write-Output "WebUI build copied to internal/webui/dist"

