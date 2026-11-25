<#
.SYNOPSIS
  Builds a release: the program and the manifest a running dash_go reads to find out that a new version is out.

.DESCRIPTION
  Writes two files into the release folder (default: <repo>\release):

      dash_go.exe    the program
      dash_go.json   the manifest: latest_version, sha256, package_url, size

  A dash_go with "update_url" set reads dash_go.json from time to time. When latest_version is not the version it
  runs, it downloads the program, checks the size and the sha256, waits until nobody is clicking or submitting,
  puts the new program in place of the old one and starts it (see docs\auto_update.md).

  The script
  1. builds the program with scripts\build.ps1 (go test, then go build with the version of the file VERSION and the
     chosen modules), so a failed test leaves the release folder as it was;
  2. asks the program for its version, measures it and hashes it;
  3. copies the program into the release folder through a temporary name and writes the manifest LAST, atomically,
     as UTF-8 without a byte order mark.
  The manifest is the last thing to change, so a service that reads it never sees a version whose program is not
  there yet. (A manual copy to a server is not atomic: a service that reads the folder while it is copied finds a
  size or a hash that does not match and tries again later.)

  The version is the one in the file VERSION. A release with the version of the one before it is not an update to any
  service: raise it first (VERSION and internal\version\version.go, see README.md). The script
  warns when it sees that.

  Publish into the static folder of the Dash API and the services read it from http://<host>/api/static/cd/dash_go.json:

      .\scripts\release.ps1 -OutDir D:\Programs\Python\static\cd

.PARAMETER OutDir
  The release folder. Default: <repo>\release.

.PARAMETER Modules
  The modules to build, as for build.ps1: -Modules hc_scan  or  -Modules a,b. Default: every module.

.PARAMETER BaseUrl
  When the services read the release over http(s) and the program is served from another address than the folder
  of the manifest, the address of the release folder, for example http://host/api/static/cd/ . package_url then
  holds the full address of the program. Without it package_url is the file name, which a service resolves next to
  the manifest it read: right for a folder or a share, and for http(s) when the program is served from the same
  folder as the manifest.

.PARAMETER SkipTests
  Do not run go test before the build.

.PARAMETER Version
  Release this version instead of the one in the file VERSION. For a rehearsal only (for example a scratch release
  folder to try an update with): a real release raises the version in the source first.

.EXAMPLE
  .\scripts\release.ps1
  .\scripts\release.ps1 -OutDir D:\Programs\Python\static\cd
  .\scripts\release.ps1 -Modules hc_scan -SkipTests
#>
[CmdletBinding()]
param(
    [string] $OutDir,
    [string[]] $Modules = @(),
    [string] $BaseUrl = '',
    [string] $Version = '',
    [switch] $SkipTests
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot

if (-not $OutDir) { $OutDir = Join-Path $repoRoot 'release' }
$OutDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutDir)

$exeName = 'dash_go.exe'
$manifestName = 'dash_go.json'
$packageUrl = $exeName
if ($BaseUrl) {
    $uri = $null
    if (-not [System.Uri]::TryCreate($BaseUrl, [System.UriKind]::Absolute, [ref]$uri) -or ($uri.Scheme -ne 'http' -and $uri.Scheme -ne 'https')) {
        throw "BaseUrl must be an http(s) address, for example http://host/api/static/cd/ , not '$BaseUrl'."
    }
    if ($uri.UserInfo -or $uri.Query -or $uri.Fragment) { throw 'BaseUrl must not contain a user name, a password, a query or a fragment.' }
    $packageUrl = $BaseUrl.TrimEnd('/') + '/' + $exeName
}

# ---- build ----
$requested = @($Modules | ForEach-Object { $_ -split '[,;\s]+' } | Where-Object { $_ } | Select-Object -Unique)
$label = if ($requested.Count -eq 0) { 'all' } else { $requested -join '+' }
$fileVersion = if ($Version) { $Version.Trim() } else { (Get-Content (Join-Path $repoRoot 'VERSION') -Raw).Trim() }
$buildArgs = @{ NoZip = $true; Version = $fileVersion }
if ($requested.Count -gt 0) { $buildArgs.Modules = $requested }
if ($SkipTests) { $buildArgs.SkipTests = $true }
& (Join-Path $PSScriptRoot 'build.ps1') @buildArgs

$built = Join-Path $repoRoot "dist\dash_go-$fileVersion-$label-windows-amd64\$exeName"
if (-not (Test-Path -LiteralPath $built -PathType Leaf)) { throw "build.ps1 did not produce $built." }

$said = (& $built -version | Select-Object -First 1)
if ($LASTEXITCODE -ne 0 -or "$said" -notmatch '^dash_go (\S+)$') { throw "The new program does not say what version it is (it printed: $said)." }
$version = $Matches[1] -replace '^[vV](?=\d)', ''
if ($version -notmatch '^[0-9][0-9A-Za-z._+-]{0,31}$') { throw "'$version' is not a version number a service accepts." }
if ($version -ne $fileVersion) { throw "The program says it is $version, but $fileVersion was to be released." }

$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $built).Hash.ToLowerInvariant()
$size = (Get-Item -LiteralPath $built).Length

# ---- publish ----
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
# The temporary program keeps the .exe extension: PowerShell will not run a file that does not have one.
$copying = Join-Path $OutDir 'dash_go.copying.exe'
$final = Join-Path $OutDir $exeName
$manifestFile = Join-Path $OutDir $manifestName
$manifestTemp = $manifestFile + '.tmp'

# The release that is in the folder now, to warn about a release that is no update.
$previous = $null
if (Test-Path -LiteralPath $manifestFile -PathType Leaf) {
    try { $previous = Get-Content -LiteralPath $manifestFile -Raw | ConvertFrom-Json } catch { $previous = $null }
}
if ($previous -and $previous.latest_version -eq $version -and $previous.sha256 -ne $hash) {
    Write-Warning "The release in $OutDir has the version $version as well, but another program. Services that run $version will not update. Raise the version (file VERSION and the other places in README.md) to release a new one."
}

try {
    Copy-Item -LiteralPath $built -Destination $copying -Force
    Move-Item -LiteralPath $copying -Destination $final -Force

    $manifest = [ordered]@{ latest_version = $version; sha256 = $hash; package_url = $packageUrl; size = [int64]$size }
    $json = ($manifest | ConvertTo-Json) + "`n"
    [System.IO.File]::WriteAllText($manifestTemp, $json, (New-Object System.Text.UTF8Encoding($false)))
    Move-Item -LiteralPath $manifestTemp -Destination $manifestFile -Force
}
finally {
    Remove-Item -LiteralPath $copying, $manifestTemp -Force -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host "Release $version"
Write-Host ("  program   {0}  ({1:N0} bytes)" -f $final, $size)
Write-Host "  sha256    $hash"
Write-Host "  manifest  $manifestFile"
Write-Host "  address   $packageUrl"
Write-Host ''
Write-Host 'The services that read this folder (update_url) and run another version download the program, wait for a'
Write-Host 'quiet moment, and restart themselves. A copy to another folder: copy the program first, the manifest last.'
