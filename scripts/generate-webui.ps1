$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$rootDir = Split-Path -Parent $PSScriptRoot
$webuiDir = Join-Path $rootDir "webui"
$embeddedDistDir = Join-Path $rootDir "internal/webui/dist"
$sourceDistDir = Join-Path $webuiDir "dist"

Push-Location $webuiDir
try {
    npm ci
    npm run build
}
finally {
    Pop-Location
}

if (Test-Path $embeddedDistDir) {
    Remove-Item -Recurse -Force $embeddedDistDir
}

Copy-Item -Recurse -Force $sourceDistDir $embeddedDistDir
Write-Host "WebUI build copied to internal/webui/dist"
