# SPDX-License-Identifier: MIT
#
# Installs the pinned SBCL on Windows: the official installer the sbcl windows/amd64 row of toolchains.lock names, verified against that
# row's SHA-256 before it runs. With -GithubEnv the install location joins PATH and SBCL_HOME / PIG_SBCL are exported to later steps.
param(
  [string]$Lock = (Join-Path $PSScriptRoot '..\dev\toolchains.lock'),
  [switch]$GithubEnv
)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$row = Get-Content $Lock | Where-Object { $_ -notmatch '^\s*#' } | ForEach-Object { , ($_ -split "`t") } | Where-Object { $_[0] -eq 'sbcl' -and $_[2] -eq 'windows' -and $_[3] -eq 'amd64' } | Select-Object -First 1
if (-not $row) { throw "toolchains.lock has no sbcl windows/amd64 row" }
$version, $url, $sha = $row[1], $row[5], $row[6]
$installer = Join-Path ([IO.Path]::GetTempPath()) "sbcl-$version.msi"
for ($attempt = 1; $attempt -le 5; $attempt++) {
  try {
    Invoke-WebRequest -Uri $url -OutFile $installer -MaximumRedirection 10 -UserAgent 'curl/8' -UseBasicParsing -TimeoutSec 300
    break
  } catch {
    Write-Host "download attempt $attempt of $url failed: $($_.Exception.Message)"
    Start-Sleep -Seconds (5 * $attempt)
  }
}
if (-not (Test-Path $installer)) { throw "could not download $url" }
$actual = (Get-FileHash -Algorithm SHA256 $installer).Hash.ToLower()
if ($actual -ne $sha) { throw "$url has SHA-256 $actual, expected $sha" }
$log = Join-Path ([IO.Path]::GetTempPath()) 'sbcl-msi.log'
$process = Start-Process msiexec -ArgumentList '/i', $installer, '/qn', '/norestart', '/log', $log -Wait -PassThru
if ($process.ExitCode -ne 0) { Get-Content $log -Tail 40; throw "msiexec exited $($process.ExitCode)" }
$home_ = 'C:\Program Files\Steel Bank Common Lisp'
$exe = Join-Path $home_ 'sbcl.exe'
if ((& $exe --version) -ne "SBCL $version") { throw "$exe is not SBCL $version" }
if ($GithubEnv) {
  Add-Content -Path $env:GITHUB_PATH -Value $home_
  Add-Content -Path $env:GITHUB_ENV -Value "SBCL_HOME=$home_"
  Add-Content -Path $env:GITHUB_ENV -Value "PIG_SBCL=$exe"
}
Write-Host "SBCL $version installed at $home_"
