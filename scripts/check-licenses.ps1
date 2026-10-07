# Copyright 2026 NomiFun Model Gateway contributors
# SPDX-License-Identifier: Apache-2.0
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$nmgRepositoryRoot = Split-Path -Parent $PSScriptRoot
$nmgGoLicensesVersion = 'v2.0.1'
$nmgAllowedLicenses = 'Apache-2.0,MIT,BSD-2-Clause,BSD-3-Clause,0BSD,ISC,Unlicense,CC0-1.0'
$nmgPreviousLocation = Get-Location
$nmgPreviousGoBin = $env:GOBIN

try {
    Set-Location -LiteralPath $nmgRepositoryRoot

    if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
        throw 'Node.js 24 or later is required for the frontend dependency license gate.'
    }
    & node (Join-Path $PSScriptRoot 'check-frontend-licenses.mjs') $nmgRepositoryRoot
    if ($LASTEXITCODE -ne 0) {
        throw 'Frontend dependency license check failed.'
    }

    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw 'Go is required. Install the version pinned in go.mod and add it to PATH.'
    }

    $nmgToolBin = Join-Path $nmgRepositoryRoot '.tools/bin'
    [void](New-Item -ItemType Directory -Path $nmgToolBin -Force)
    $env:GOBIN = $nmgToolBin
    & go install "github.com/google/go-licenses/v2@$nmgGoLicensesVersion"
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to install go-licenses $nmgGoLicensesVersion."
    }

    $nmgToolName = if ($env:OS -eq 'Windows_NT') { 'go-licenses.exe' } else { 'go-licenses' }
    & (Join-Path $nmgToolBin $nmgToolName) check --include_tests "--allowed_licenses=$nmgAllowedLicenses" ./...
    if ($LASTEXITCODE -ne 0) {
        throw 'Go dependency license check failed.'
    }
    Write-Host 'Go license gate passed, including imported transitive and test dependencies.'
}
finally {
    $env:GOBIN = $nmgPreviousGoBin
    Set-Location -LiteralPath $nmgPreviousLocation.Path
}
