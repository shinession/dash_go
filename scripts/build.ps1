<#
.SYNOPSIS
  Builds dash_go and packages the selected modules.

.DESCRIPTION
  Every module lives in internal/mods/<name> and is linked in by cmd/dash_go/mod_<name>.go, which carries the
  build constraint "!nomod_<name>". This script adds a nomod_<name> tag for every module that was not selected,
  so the executable contains exactly the chosen modules and nothing else.

  Without -Modules every module is packaged.

.PARAMETER Modules
  Modules to package, comma separated: -Modules hc_scan  or  -Modules a,b,c

.PARAMETER List
  Print the available modules and exit.

.EXAMPLE
  .\scripts\build.ps1
  .\scripts\build.ps1 -Modules hc_scan
  .\scripts\build.ps1 -Modules hc_scan,other -OutDir D:\release
  .\scripts\build.ps1 -List
#>
[CmdletBinding()]
param(
    [string[]] $Modules = @(),
    [string] $OutDir = 'dist',
    [string] $Version = '',
    [string] $GOOS = 'windows',
    [string] $GOARCH = 'amd64',
    [switch] $List,
    [switch] $NoZip,
    [switch] $SkipTests
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

function Find-Go {
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    foreach ($base in @($env:GOROOT, (Join-Path $env:ProgramFiles 'Go'))) {
        if ($base) {
            $candidate = Join-Path $base 'bin\go.exe'
            if (Test-Path $candidate) { return $candidate }
        }
    }
    throw 'Go was not found. Install Go, or add its bin folder to PATH.'
}

# ---- discover the modules ----
$available = [ordered]@{}
foreach ($dir in Get-ChildItem (Join-Path $root 'internal\mods') -Directory | Sort-Object Name) {
    $manifestPath = Join-Path $dir.FullName 'manifest.json'
    if (-not (Test-Path $manifestPath)) { continue }
    $manifest = Get-Content $manifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($manifest.name -ne $dir.Name) { throw "manifest of $($dir.Name) names the module '$($manifest.name)'" }
    if (-not (Test-Path (Join-Path $root "cmd\dash_go\mod_$($dir.Name).go"))) { throw "cmd\dash_go\mod_$($dir.Name).go is missing, the module would never be linked in" }
    $available[$dir.Name] = $manifest
}

if ($List) {
    Write-Output 'Available modules:'
    foreach ($m in $available.Values) { Write-Output ('  {0,-14} {1}' -f $m.name, $m.title) }
    return
}

$requested = @($Modules | ForEach-Object { $_ -split '[,;\s]+' } | Where-Object { $_ })
$allSelected = ($requested.Count -eq 0)
$selected = if ($allSelected) { @($available.Keys) } else { @($requested | Select-Object -Unique) }
$unknown = @($selected | Where-Object { -not $available.Contains($_) })
if ($unknown.Count -gt 0) { throw "Unknown module(s): $($unknown -join ', '). Available: $($available.Keys -join ', ')" }
$excluded = @($available.Keys | Where-Object { $selected -notcontains $_ })

if (-not $Version) { $Version = (Get-Content (Join-Path $root 'VERSION') -Raw).Trim() }
$go = Find-Go

# ---- test, then build ----
if (-not $SkipTests) {
    Write-Output 'Running the tests ...'
    & $go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'The tests failed; nothing was built.' }
}

$label = if ($allSelected) { 'all' } else { ($selected -join '+') }
$name = "dash_go-$Version-$label-$GOOS-$GOARCH"
$pkgDir = Join-Path (Join-Path $root $OutDir) $name
if (Test-Path $pkgDir) { Remove-Item $pkgDir -Recurse -Force }
New-Item -ItemType Directory -Path $pkgDir | Out-Null

$exeName = if ($GOOS -eq 'windows') { 'dash_go.exe' } else { 'dash_go' }
$tags = ($excluded | ForEach-Object { "nomod_$_" }) -join ','
$ldflags = "-s -w -X dash_go/internal/version.Version=$Version -X dash_go/internal/version.Modules=$($selected -join ',')"
Write-Output "Building $name (modules: $($selected -join ', '))"
$env:GOOS = $GOOS; $env:GOARCH = $GOARCH; $env:CGO_ENABLED = '0'
try {
    $buildArgs = @('build', '-trimpath', '-ldflags', $ldflags, '-o', (Join-Path $pkgDir $exeName), './cmd/dash_go')
    if ($tags) { $buildArgs = @('build', '-trimpath', '-tags', $tags, '-ldflags', $ldflags, '-o', (Join-Path $pkgDir $exeName), './cmd/dash_go') }
    & $go @buildArgs
    if ($LASTEXITCODE -ne 0) { throw 'go build failed.' }
} finally {
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}

# ---- verify what was linked in (only possible when the target runs on this machine) ----
if ($GOOS -eq 'windows' -and $env:OS -eq 'Windows_NT') {
    $reported = (& (Join-Path $pkgDir $exeName) -version | Select-String '^modules:').ToString() -replace '^modules:\s*', ''
    $linked = @($reported -split ',\s*' | Where-Object { $_ })
    if ((Compare-Object $linked $selected).Count -ne 0) { throw "The executable contains [$($linked -join ', ')] but [$($selected -join ', ')] was requested." }
}

# ---- package contents ----
$configDir = Join-Path $pkgDir 'config'
New-Item -ItemType Directory -Path $configDir | Out-Null
$example = Get-Content (Join-Path $root 'config\dash_go.example.json') -Raw -Encoding UTF8 | ConvertFrom-Json
foreach ($prop in @($example.modules.PSObject.Properties.Name)) {
    if ($selected -notcontains $prop) { $example.modules.PSObject.Properties.Remove($prop) }
}
$example | ConvertTo-Json -Depth 10 | Set-Content (Join-Path $configDir 'dash_go.example.json') -Encoding UTF8

New-Item -ItemType Directory -Path (Join-Path $pkgDir 'deploy') | Out-Null
Copy-Item (Join-Path $root 'deploy\nginx_dash_go.conf.example') (Join-Path $pkgDir 'deploy')
$docsDir = Join-Path $pkgDir 'docs'
New-Item -ItemType Directory -Path $docsDir | Out-Null
foreach ($m in $selected) {
    foreach ($doc in $available[$m].docs) { Copy-Item (Join-Path $root $doc) $docsDir }
}
# the frame every module uses (header, theme, language, sign-in) and the automatic update are in every package
Copy-Item (Join-Path $root 'docs\shell_and_login.md') $docsDir
Copy-Item (Join-Path $root 'docs\auto_update.md') $docsDir
if (Test-Path (Join-Path $root 'README.md')) { Copy-Item (Join-Path $root 'README.md') $pkgDir }
Set-Content (Join-Path $pkgDir 'VERSION') $Version -Encoding ASCII
$lines = @("dash_go $Version", "target: $GOOS/$GOARCH", 'modules:') + @($selected | ForEach-Object { "  - ${_}: $($available[$_].title)" })
Set-Content (Join-Path $pkgDir 'MODULES.txt') $lines -Encoding UTF8

if (-not $NoZip) {
    $zip = Join-Path (Join-Path $root $OutDir) "$name.zip"
    if (Test-Path $zip) { Remove-Item $zip -Force }
    Compress-Archive -Path (Join-Path $pkgDir '*') -DestinationPath $zip
    Write-Output "Package: $zip"
}
Write-Output "Folder:  $pkgDir"
