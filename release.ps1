# Baut nexora/plan fuer Linux (amd64) und veroeffentlicht es als GitHub-Release.
# NexoraPanel laedt das Binary von dort (Domain -> Apps -> "Von GitHub installieren").
#
# Aufruf:  .\release.ps1                      # Version = heutiges Datum
#          .\release.ps1 -Version 2026.10.22  # Version fest vorgeben
#
# Release-Dateien (Namen erwartet das Panel genau so):
#   nexoraplan-linux-amd64          das Programm
#   nexoraplan-linux-amd64.sha256   Pruefsumme ("<hash>  nexoraplan-linux-amd64")
#
# Voraussetzungen: gh (angemeldet), go, npm. Der Release zeigt auf den aktuellen
# Stand von main auf GitHub - vorher committen und pushen.
param([string]$Version = "")
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$Asset = "nexoraplan-linux-amd64"

# Nur aus sauberem, gepushtem Stand veroeffentlichen - sonst passt der Release
# nicht zum Quellcode auf GitHub.
if (git status --porcelain) { throw "Es gibt uncommittete Aenderungen - erst committen." }
git fetch -q origin main
# Von einem Feature-Branch aus (z.B. nach gemergtem PR) automatisch auf main
# wechseln und vorspulen. Nur Fast-Forward - lokale, ungepushte Commits auf
# main fuehren weiter zum Abbruch.
if ((git branch --show-current) -ne "main") {
    Write-Host "==> Wechsle auf main ..."
    git switch -q main
    if ($LASTEXITCODE) { throw "Wechsel auf main fehlgeschlagen." }
}
git merge -q --ff-only origin/main
if ($LASTEXITCODE) { throw "main laesst sich nicht auf origin/main vorspulen - lokale Commits erst pushen." }
if ((git rev-parse HEAD) -ne (git rev-parse origin/main)) { throw "Lokaler Stand ist nicht gleich origin/main - erst pushen/pullen." }

if (-not $Version) {
    $Version = Get-Date -Format "yyyy.MM.dd"
    $last = (gh release list --limit 1 --json tagName --jq '.[0].tagName' 2>$null)
    if ($last) { $last = $last.Trim().TrimStart("v") }
    if ($last -eq $Version -or ($last -and $last.StartsWith("$Version."))) {
        $n = if ($last -eq $Version) { 2 } else { [int]$last.Substring($Version.Length + 1) + 1 }
        $Version = "$Version.$n"
    }
}
$Tag = "v$Version"
Write-Host "==> Release $Tag"

Write-Host "==> Baue Oberflaeche (SvelteKit) ..."
$env:PLAN_VERSION = $Version
Push-Location frontend
try {
    if (-not (Test-Path node_modules)) {
        npm ci
        if ($LASTEXITCODE -ne 0) { throw "npm ci fehlgeschlagen" }
    }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "Oberflaeche konnte nicht gebaut werden" }
}
finally {
    Pop-Location
    Remove-Item Env:PLAN_VERSION -ErrorAction SilentlyContinue
}

Write-Host "==> Baue Linux-Binary ..."
New-Item -ItemType Directory -Force build | Out-Null
$env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
try {
    go build -trimpath -ldflags "-s -w -X nexoraplan/internal/server.Version=$Version" -o "build/$Asset" ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw "Build fehlgeschlagen" }
}
finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}

# Pruefsumme im sha256sum-Format, LF-Zeilenende, ohne BOM.
$hash = (Get-FileHash "build/$Asset" -Algorithm SHA256).Hash.ToLower()
[System.IO.File]::WriteAllText("$PSScriptRoot/build/$Asset.sha256", "$hash  $Asset`n")

Write-Host "==> Veroeffentliche auf GitHub ..."
gh release create $Tag "build/$Asset" "build/$Asset.sha256" --target main --title "nexora/plan $Version" --generate-notes
if ($LASTEXITCODE -ne 0) { throw "gh release create fehlgeschlagen" }

Write-Host "==> Fertig: $Tag ($hash)"
