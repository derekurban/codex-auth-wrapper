$ErrorActionPreference = "Stop"

param(
  [string]$Version = $env:VERSION,
  [string]$InstallDir = $(if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:USERPROFILE "bin" }),
  [switch]$NoCheck
)

$Repo = "derekurban/codex-auth-wrapper"
$Binary = "caw"

function Resolve-Version {
  param([string]$RequestedVersion)
  if ($RequestedVersion) {
    return $RequestedVersion
  }

  $response = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest"
  if (-not $response.tag_name) {
    throw "Unable to resolve latest release version."
  }
  return $response.tag_name
}

function Resolve-Arch {
  switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { return "amd64" }
    "ARM64" { return "arm64" }
    default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
  }
}

$ResolvedVersion = Resolve-Version -RequestedVersion $Version
$Arch = Resolve-Arch
$AssetName = "${Binary}_${ResolvedVersion}_windows_${Arch}.zip"
$ReleaseBase = "https://github.com/$Repo/releases/download/$ResolvedVersion"

$TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $TempDir | Out-Null

try {
  $ArchivePath = Join-Path $TempDir $AssetName
  $ChecksumsPath = Join-Path $TempDir "checksums.txt"

  Invoke-WebRequest -Uri "$ReleaseBase/$AssetName" -OutFile $ArchivePath
  Invoke-WebRequest -Uri "$ReleaseBase/checksums.txt" -OutFile $ChecksumsPath

  $ExpectedHash = (
    Get-Content $ChecksumsPath |
      Where-Object { $_ -match [regex]::Escape($AssetName) } |
      ForEach-Object { ($_ -split '\s+')[0] } |
      Select-Object -First 1
  )
  if (-not $ExpectedHash) {
    throw "Unable to locate checksum for $AssetName"
  }

  $ActualHash = (Get-FileHash -Algorithm SHA256 -Path $ArchivePath).Hash.ToLowerInvariant()
  if ($ActualHash -ne $ExpectedHash.ToLowerInvariant()) {
    throw "Checksum mismatch for $AssetName"
  }

  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  Expand-Archive -Path $ArchivePath -DestinationPath $TempDir -Force

  $BinaryPath = Get-ChildItem -Path $TempDir -Recurse -Filter "$Binary.exe" | Select-Object -First 1
  if (-not $BinaryPath) {
    throw "Extracted archive did not contain $Binary.exe"
  }

  $Destination = Join-Path $InstallDir "$Binary.exe"
  Copy-Item -Path $BinaryPath.FullName -Destination $Destination -Force
  Write-Host "Installed $Binary $ResolvedVersion to $Destination"

  if (-not $NoCheck) {
    & $Destination --version
  }
}
finally {
  if (Test-Path $TempDir) {
    Remove-Item -Recurse -Force $TempDir
  }
}
