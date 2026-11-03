# build.ps1 — compila los dos binarios de VPNPrint ( amd64 + 386 )
# Requiere Go 1.20 o superior en el PATH (usar gofurnir: go.dev/dl)
$ErrorActionPreference = "Stop"
$ld = "-s -w -H=windowsgui"
$env:GOCACHE = "$env:TEMP\gocache"

$go = "go"
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $alt = "$env:TEMP\goforbuild\go\bin\go.exe"
    if (Test-Path $alt) { $go = $alt }
    else { throw "no hay go instalado" }
}

& $go build -trimpath -ldflags $ld -o vpnprint-x64.exe .
$env:GOARCH = "386"
& $go build -trimpath -ldflags $ld -o vpnprint-x86.exe .
$env:GOARCH = "amd64"
Write-Host "listo:"
Get-ChildItem vpnprint-*.exe | ForEach-Object { "  $($_.Name)  $([math]::Round($_.Length/1MB,1)) MB" }
