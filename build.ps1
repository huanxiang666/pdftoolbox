#!/usr/bin/env pwsh
# 本地构建 pdftoolbox Windows 发布包
# 用法: .\build.ps1 [-Version 1.0.0]

param(
    [string]$Version = "1.0.0"
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    foreach ($p in @("C:\Program Files\Go\bin", "$env:LOCALAPPDATA\Programs\Go\bin")) {
        if (Test-Path (Join-Path $p "go.exe")) { $env:Path = "$p;$env:Path"; break }
    }
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "找不到 go 命令，请先安装 Go 1.22+"
}

Write-Host "=== pdftoolbox $Version Windows amd64 ===" -ForegroundColor Cyan
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

$dist = Join-Path $root "dist"
if (Test-Path $dist) { Remove-Item $dist -Recurse -Force }
New-Item -ItemType Directory -Path $dist | Out-Null

Write-Host "[1/3] 编译 + 测试..." -ForegroundColor Yellow
go test ./... 2>&1 | Select-String -Pattern "FAIL|ok " | ForEach-Object { "  $_" }
go build -trimpath -ldflags "-s -w -X main.Version=$Version" -o "$dist\pdftoolbox.exe" ./cmd/pdftoolbox
if ($LASTEXITCODE -ne 0) { throw "编译失败" }

Write-Host "[2/3] 冒烟测试..." -ForegroundColor Yellow
& "$dist\pdftoolbox.exe" --version
& "$dist\pdftoolbox.exe" check | Select-Object -First 4

Write-Host "[3/3] 打包..." -ForegroundColor Yellow
$name = "pdftoolbox-$Version-windows-amd64"
$pkg = Join-Path $root "pkg\$name"
New-Item -ItemType Directory -Path $pkg -Force | Out-Null
Copy-Item "$dist\pdftoolbox.exe" $pkg
foreach ($f in @("README.md", "LICENSE", "THIRD-PARTY-NOTICES.md")) {
    $src = Join-Path $root $f
    if (Test-Path $src) { Copy-Item $src $pkg }
}
$zip = Join-Path $root "pkg\$name.zip"
if (Test-Path $zip) { Remove-Item $zip -Force }
Compress-Archive -Path $pkg -DestinationPath $zip

Write-Host "`n完成: $zip ($([math]::Round((Get-Item $zip).Length/1MB,2)) MB)" -ForegroundColor Green
Write-Host "二进制: $([math]::Round((Get-Item "$dist\pdftoolbox.exe").Length/1MB,2)) MB" -ForegroundColor Green
