# Installs the skael CLI on Windows for the current user. No admin rights.
#   irm https://skael.dev/install.ps1 | iex
# A version can be pinned with $env:SKAEL_VERSION = "0.22.0" first.
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$Repo = "skael-dev/skael"
$InstallDir = if ($env:SKAEL_INSTALL_DIR) { $env:SKAEL_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\skael\bin" }

$Arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  default { throw "skael: unsupported architecture $env:PROCESSOR_ARCHITECTURE" }
}

$Version = $env:SKAEL_VERSION
if (-not $Version) {
  try {
    $Release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers @{ "User-Agent" = "skael-installer" }
  } catch {
    throw "skael: could not read the latest release from the GitHub API. Retry in a minute, or download a release from https://github.com/$Repo/releases/latest"
  }
  $Version = $Release.tag_name.TrimStart("v")
}
$Version = $Version.TrimStart("v")

$File = "skael_${Version}_windows_${Arch}.zip"
$Base = "https://github.com/$Repo/releases/download/v$Version"
$Tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("skael-" + [System.Guid]::NewGuid())
New-Item -ItemType Directory -Path $Tmp | Out-Null
try {
  Write-Host "Downloading skael v$Version for windows/$Arch..."
  Invoke-WebRequest -Uri "$Base/$File" -OutFile (Join-Path $Tmp $File) -UseBasicParsing
  Invoke-WebRequest -Uri "$Base/checksums.txt" -OutFile (Join-Path $Tmp "checksums.txt") -UseBasicParsing

  $Line = Get-Content (Join-Path $Tmp "checksums.txt") | Where-Object { $_ -match "  $([regex]::Escape($File))$" } | Select-Object -First 1
  if (-not $Line) { throw "skael: $File is not in checksums.txt" }
  $Expected = ($Line -split "\s+")[0].ToLower()
  $Actual = (Get-FileHash -Algorithm SHA256 (Join-Path $Tmp $File)).Hash.ToLower()
  if ($Expected -ne $Actual) { throw "skael: checksum mismatch for $File (expected $Expected, got $Actual)" }

  Expand-Archive -Path (Join-Path $Tmp $File) -DestinationPath (Join-Path $Tmp "x") -Force
  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  Copy-Item -Path (Join-Path $Tmp "x\skael.exe") -Destination (Join-Path $InstallDir "skael.exe") -Force
} finally {
  Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}

$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
$Entries = @($UserPath -split ";" | Where-Object { $_ -ne "" })
if ($Entries -notcontains $InstallDir) {
  [Environment]::SetEnvironmentVariable("Path", (($Entries + $InstallDir) -join ";"), "User")
  Write-Host "Added $InstallDir to your user PATH. New terminals pick it up."
}
if (($env:Path -split ";") -notcontains $InstallDir) {
  $env:Path = "$env:Path;$InstallDir"
}

Write-Host "Installed skael v$Version to $InstallDir\skael.exe"
Write-Host "Next: skael setup <server-url> <api-key>"
