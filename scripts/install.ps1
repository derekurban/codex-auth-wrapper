param(
  [string]$Version = $env:VERSION,
  [string]$InstallDir = $env:INSTALL_DIR,
  [string]$CacheDir = $env:CAW_CACHE_DIR,
  [switch]$NoCheck
)

$ErrorActionPreference = "Stop"

$Repo = "derekurban/codex-auth-wrapper"
$Binary = "caw"

if (-not $InstallDir) {
  $InstallDir = Join-Path $env:USERPROFILE "bin"
}

if (-not $CacheDir) {
  if ($env:LOCALAPPDATA) {
    $CacheDir = Join-Path (Join-Path $env:LOCALAPPDATA "codex-auth-wrapper") "cache"
  }
  else {
    $CacheDir = Join-Path ([System.IO.Path]::GetTempPath()) "codex-auth-wrapper-cache"
  }
}

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

function Download-IfMissing {
  param(
    [string]$Uri,
    [string]$OutFile
  )

  if (-not (Test-Path $OutFile)) {
    Write-Host "Downloading $(Split-Path -Leaf $OutFile)..."
    Invoke-WebRequest -Uri $Uri -OutFile $OutFile
    return
  }
  Write-Host "Using cached $(Split-Path -Leaf $OutFile)..."
}

function Download-Force {
  param(
    [string]$Uri,
    [string]$OutFile
  )

  Write-Host "Re-downloading $(Split-Path -Leaf $OutFile)..."
  Invoke-WebRequest -Uri $Uri -OutFile $OutFile
}

function Normalize-PathEntry {
  param([string]$PathEntry)
  if (-not $PathEntry) {
    return $null
  }
  $Normalized = $PathEntry.Trim()
  if (-not $Normalized) {
    return $null
  }
  $Normalized = $Normalized -replace '[\\/]+$',''
  return $Normalized.ToLowerInvariant()
}

function Path-ContainsEntry {
  param(
    [string]$PathValue,
    [string]$Candidate
  )

  $NormalizedCandidate = Normalize-PathEntry $Candidate
  if (-not $NormalizedCandidate) {
    return $false
  }

  foreach ($Entry in ($PathValue -split ';')) {
    if ((Normalize-PathEntry $Entry) -eq $NormalizedCandidate) {
      return $true
    }
  }

  return $false
}

function Ensure-PathContains {
  param([string]$Directory)

  if (-not (Path-ContainsEntry -PathValue $env:Path -Candidate $Directory)) {
    $env:Path = if ($env:Path) { "$Directory;$env:Path" } else { $Directory }
    Write-Host "Added $Directory to the current session PATH"
  }

  $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if (-not (Path-ContainsEntry -PathValue $UserPath -Candidate $Directory)) {
    $UpdatedUserPath = if ($UserPath) { "$Directory;$UserPath" } else { $Directory }
    [Environment]::SetEnvironmentVariable("Path", $UpdatedUserPath, "User")
    Write-Host "Added $Directory to the user PATH"
  }
}

function Get-CawProcesses {
  Get-Process -Name $Binary -ErrorAction SilentlyContinue
}

function Stop-RunningCAW {
  param([string]$BinaryPath)

  if (-not (Test-Path $BinaryPath)) {
    return
  }

  $HadProcesses = @(Get-CawProcesses).Count -gt 0
  if (-not $HadProcesses) {
    return
  }

  Write-Host "Stopping running caw processes..."

  try {
    & $BinaryPath shutdown | Out-Null
  }
  catch {
    try {
      & $BinaryPath broker stop | Out-Null
    }
    catch {
    }
  }

  for ($i = 0; $i -lt 40; $i++) {
    $Processes = @(Get-CawProcesses)
    if ($Processes.Count -eq 0) {
      return
    }
    Start-Sleep -Milliseconds 250
  }

  throw "caw.exe is still running. Close all caw windows and retry the installer."
}

$ResolvedVersion = Resolve-Version -RequestedVersion $Version
$AssetVersion = $ResolvedVersion -replace '^v', ''
$Arch = Resolve-Arch
$AssetName = "${Binary}_${AssetVersion}_windows_${Arch}.zip"
$ReleaseBase = "https://github.com/$Repo/releases/download/$ResolvedVersion"
$VersionCacheDir = Join-Path $CacheDir $ResolvedVersion

$TempDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $TempDir | Out-Null

try {
  New-Item -ItemType Directory -Force -Path $VersionCacheDir | Out-Null
  $ArchivePath = Join-Path $VersionCacheDir $AssetName
  $ChecksumsPath = Join-Path $VersionCacheDir "checksums.txt"
  $ExtractDir = Join-Path $TempDir "extract"

  Download-IfMissing -Uri "$ReleaseBase/checksums.txt" -OutFile $ChecksumsPath
  Download-IfMissing -Uri "$ReleaseBase/$AssetName" -OutFile $ArchivePath

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
    Remove-Item -Force $ArchivePath -ErrorAction SilentlyContinue
    Download-Force -Uri "$ReleaseBase/$AssetName" -OutFile $ArchivePath
    $ActualHash = (Get-FileHash -Algorithm SHA256 -Path $ArchivePath).Hash.ToLowerInvariant()
    if ($ActualHash -ne $ExpectedHash.ToLowerInvariant()) {
      throw "Checksum mismatch for $AssetName"
    }
  }

  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  Expand-Archive -Path $ArchivePath -DestinationPath $ExtractDir -Force

  $BinaryPath = Get-ChildItem -Path $ExtractDir -Recurse -Filter "$Binary.exe" | Select-Object -First 1
  if (-not $BinaryPath) {
    throw "Extracted archive did not contain $Binary.exe"
  }

  $Destination = Join-Path $InstallDir "$Binary.exe"
  $ShimPath = Join-Path $InstallDir "$Binary.cmd"
  Stop-RunningCAW -BinaryPath $Destination
  Copy-Item -Path $BinaryPath.FullName -Destination $Destination -Force
  Set-Content -Path $ShimPath -Value "@echo off`r`n""%~dp0$Binary.exe"" %*`r`n" -NoNewline
  Ensure-PathContains -Directory $InstallDir
  Write-Host "Installed $Binary $ResolvedVersion to $Destination"
  Write-Host "Installed command shim to $ShimPath"
  Write-Host "Using release cache at $VersionCacheDir"

  if (-not $NoCheck) {
    & $Binary --version
  }
}
finally {
  if (Test-Path $TempDir) {
    Remove-Item -Recurse -Force $TempDir
  }
}
