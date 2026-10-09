# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# Installs the pinned native tools in windows-tools.txt without a package feed.
# Archives download from GitHub releases into $Cache (restored by actions/cache),
# are verified against the committed SHA-256, and extract into $Bin, which joins
# PATH. A mismatched cached archive is fetched again, and a failed download is
# retried, for up to five attempts; an archive still missing or mismatched after
# them is fatal.
param(
  [string]$Manifest = (Join-Path $PSScriptRoot 'windows-tools.txt'),
  [Parameter(Mandatory)][string]$Cache,
  [Parameter(Mandatory)][string]$Bin
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
New-Item -ItemType Directory -Force -Path $Cache, $Bin | Out-Null

function Get-Verified([string]$Url, [string]$Path, [string]$Sha256) {
  for ($attempt = 1; $attempt -le 5; $attempt++) {
    if (Test-Path $Path) {
      if ((Get-FileHash -Algorithm SHA256 $Path).Hash -eq $Sha256) { return }
      Remove-Item -Force $Path
    }
    try {
      Invoke-WebRequest -Uri $Url -OutFile $Path -UseBasicParsing -TimeoutSec 120
    } catch {
      Write-Host "download attempt $attempt of $Url failed: $($_.Exception.Message)"
      if (Test-Path $Path) { Remove-Item -Force $Path }
      Start-Sleep -Seconds (5 * $attempt)
    }
  }
  if (-not (Test-Path $Path)) { throw "could not download $Url" }
  $actual = (Get-FileHash -Algorithm SHA256 $Path).Hash
  if ($actual -ne $Sha256) { throw "$Url has SHA-256 $actual, expected $Sha256" }
}

foreach ($line in Get-Content $Manifest) {
  if ($line -match '^\s*(#|$)') { continue }
  $tool, $exe, $sha, $url = -split $line
  $sha = $sha.ToUpperInvariant()
  $zip = Join-Path $Cache ([IO.Path]::GetFileName($url))
  Get-Verified $url $zip $sha
  $dest = Join-Path (Split-Path -Parent $Bin) "extract-$tool"
  if (Test-Path $dest) { Remove-Item -Recurse -Force $dest }
  Expand-Archive -Path $zip -DestinationPath $dest
  $found = Get-ChildItem $dest -Recurse -Filter $exe | Select-Object -First 1
  if (-not $found) { throw "$exe not found in $url" }
  Copy-Item -Force $found.FullName (Join-Path $Bin $exe)
}
