# build-payload.ps1
# Assemble payload.zip: node/ (Node.js runtime) + dsh/ (installed DSH).
# Output is written to payload/payload.zip for Go's go:embed.
#
# Usage:
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-payload.ps1
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-payload.ps1 -Version v1.0.0
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-payload.ps1 -NodeDir "C:\Program Files\nodejs" -DshDir "E:\...\@deepseek-ai\dsh"

param(
    [string]$NodeDir = "",
    [string]$DshDir  = "",
    [string]$Output  = "",
    [string]$Version = "dev",
    [ValidateSet("Optimal", "Fastest", "NoCompression")]
    [string]$Compression = "Optimal"
)

$ErrorActionPreference = "Stop"

function Test-DshDir {
    param([string]$Path)
    if (-not $Path) { return $false }
    return ((Test-Path (Join-Path $Path "lib\bin.js")) -and (Test-Path (Join-Path $Path "node_modules")))
}

function Resolve-NodeDir {
    if ($NodeDir) {
        $candidate = (Resolve-Path $NodeDir -ErrorAction SilentlyContinue).Path
        if ($candidate -and (Test-Path (Join-Path $candidate "node.exe"))) { return $candidate }
    }
    $nodeExe = $null
    $cmd = Get-Command node -ErrorAction SilentlyContinue
    if ($cmd) { $nodeExe = $cmd.Source }
    if (-not $nodeExe) {
        $where = (& where.exe node 2>$null | Select-Object -First 1)
        if ($where) { $nodeExe = $where.Trim() }
    }
    if ($nodeExe -and (Test-Path $nodeExe)) {
        return (Split-Path $nodeExe -Parent)
    }
    throw "Node.js (node.exe) not found. Pass its directory via -NodeDir."
}

function Resolve-DshDir {
    if ($DshDir) {
        $candidate = (Resolve-Path $DshDir -ErrorAction SilentlyContinue).Path
        if (Test-DshDir $candidate) { return $candidate }
    }
    # 1) Derive from the 'dsh' command on PATH: the npm/pnpm shim lives in the
    #    global prefix directory, and the real package is at
    #    <prefix>\node_modules\@deepseek-ai\dsh.
    $cmd = Get-Command dsh -ErrorAction SilentlyContinue
    if ($cmd -and $cmd.Source) {
        $candidate = Join-Path (Split-Path $cmd.Source -Parent) "node_modules\@deepseek-ai\dsh"
        if (Test-DshDir $candidate) { return $candidate }
    }
    # 2) Try the npm / pnpm global node_modules roots.
    $roots = @()
    foreach ($tool in @("npm", "pnpm")) {
        if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { continue }
        try {
            $root = (& $tool root -g 2>$null | Select-Object -First 1)
            if ($root) { $roots += $root.Trim() }
        } catch { }
    }
    foreach ($root in $roots) {
        if (-not $root) { continue }
        $candidate = Join-Path $root "@deepseek-ai\dsh"
        if (Test-DshDir $candidate) { return $candidate }
    }
    throw "Installed DSH (@deepseek-ai/dsh) not found. Pass its directory via -DshDir."
}

$nodeDir = Resolve-NodeDir
$dshDir  = Resolve-DshDir

$repoRoot = Split-Path $PSScriptRoot -Parent
$outputPath = $Output
if (-not $outputPath) { $outputPath = Join-Path $repoRoot "payload\payload.zip" }
$outputPath = [System.IO.Path]::GetFullPath($outputPath)

Write-Host "Node dir : $nodeDir"
Write-Host "DSH dir  : $dshDir"
Write-Host "Output   : $outputPath"
Write-Host "Version  : $Version"

$staging = Join-Path ([System.IO.Path]::GetTempPath()) ("harnessbox-payload-" + [guid]::NewGuid().ToString("N"))
$stageNode = Join-Path $staging "node"
$stageDsh  = Join-Path $staging "dsh"
try {
    New-Item -ItemType Directory -Force -Path $stageNode, $stageDsh | Out-Null

    # Node.js runtime: only what is needed to run, keeping its LICENSE.
    foreach ($name in @("node.exe", "LICENSE", "README.md", "CHANGELOG.md")) {
        $src = Join-Path $nodeDir $name
        if (Test-Path $src) { Copy-Item $src -Destination (Join-Path $stageNode $name) -Force }
    }
    if (-not (Test-Path (Join-Path $stageNode "node.exe"))) {
        throw "node.exe missing in Node dir: $nodeDir"
    }

    # DSH: copy the whole install, preserving directory layout and third-party LICENSE files.
    Copy-Item -Path (Join-Path $dshDir "*") -Destination $stageDsh -Recurse -Force
    if (-not (Test-Path (Join-Path $stageDsh "lib\bin.js"))) {
        throw "lib\bin.js missing in DSH dir: $dshDir"
    }

    # payload version marker, shipped inside the zip.
    $meta = [ordered]@{
        version = $Version
        node    = (Split-Path $nodeDir -Leaf)
        dsh     = (Split-Path $dshDir -Leaf)
    }
    $meta | ConvertTo-Json | Set-Content -Path (Join-Path $staging "payload.json") -Encoding UTF8

    $outDir = Split-Path $outputPath -Parent
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null
    if (Test-Path $outputPath) { Remove-Item $outputPath -Force }

    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $level = [System.IO.Compression.CompressionLevel]::$Compression
    [System.IO.Compression.ZipFile]::CreateFromDirectory($staging, $outputPath, $level, $false)

    $sizeMB = [math]::Round((Get-Item $outputPath).Length / 1MB, 1)
    Write-Host "Built: $outputPath ($sizeMB MB)"
}
finally {
    if (Test-Path $staging) { Remove-Item $staging -Recurse -Force -ErrorAction SilentlyContinue }
}
