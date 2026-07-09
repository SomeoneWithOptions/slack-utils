param(
    [string]$Repo = "SomeoneWithOptions/slack-utils",
    [string]$Version = "latest",
    [string]$InstallDir = $(Join-Path $env:LOCALAPPDATA "Programs\slack-utils"),
    [switch]$NoPathUpdate
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
} catch {
    # PowerShell Core does not need this on modern Windows.
}

function Fail([string]$Message) {
    Write-Error "error: $Message"
    exit 1
}

function Download-File([string]$Url, [string]$Path) {
    try {
        Invoke-WebRequest -Uri $Url -OutFile $Path -UseBasicParsing
    } catch {
        Fail "failed to download $Url"
    }
}

function Test-PathContains([string]$PathValue, [string]$Directory) {
    if ([string]::IsNullOrWhiteSpace($PathValue)) {
        return $false
    }

    $target = [System.IO.Path]::GetFullPath($Directory).TrimEnd('\')
    foreach ($entry in ($PathValue -split ';')) {
        if ([string]::IsNullOrWhiteSpace($entry)) {
            continue
        }

        try {
            $fullEntry = [System.IO.Path]::GetFullPath($entry).TrimEnd('\')
            if ($fullEntry -ieq $target) {
                return $true
            }
        } catch {
            if ($entry.TrimEnd('\') -ieq $Directory.TrimEnd('\')) {
                return $true
            }
        }
    }

    return $false
}

$archEnv = $env:PROCESSOR_ARCHITEW6432
if ([string]::IsNullOrWhiteSpace($archEnv)) {
    $archEnv = $env:PROCESSOR_ARCHITECTURE
}

switch -Regex ($archEnv) {
    '^(AMD64|x86_64)$' { $arch = "amd64"; break }
    '^ARM64$' { $arch = "arm64"; break }
    default { Fail "unsupported architecture: $archEnv" }
}

$asset = "slack-utils_windows_$arch.zip"
if ($Version -eq "latest") {
    $baseUrl = "https://github.com/$Repo/releases/latest/download"
} else {
    $baseUrl = "https://github.com/$Repo/releases/download/$Version"
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("slack-utils-install-" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

try {
    $archivePath = Join-Path $tmp $asset
    $checksumsPath = Join-Path $tmp "checksums.txt"

    Write-Host "Downloading $asset..."
    Download-File "$baseUrl/$asset" $archivePath
    Download-File "$baseUrl/checksums.txt" $checksumsPath

    $expected = Get-Content $checksumsPath | ForEach-Object {
        $parts = $_ -split '\s+'
        if ($parts.Length -ge 2 -and $parts[-1] -eq $asset) {
            $parts[0].ToLowerInvariant()
        }
    } | Select-Object -First 1

    if ([string]::IsNullOrWhiteSpace($expected)) {
        Fail "checksum for $asset not found"
    }

    $actual = (Get-FileHash -Algorithm SHA256 $archivePath).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        Fail "checksum mismatch for $asset"
    }

    Expand-Archive -LiteralPath $archivePath -DestinationPath $tmp -Force
    $binaryPath = Join-Path $tmp "slack-utils.exe"
    if (-not (Test-Path -LiteralPath $binaryPath)) {
        Fail "archive did not contain slack-utils.exe"
    }

    Write-Host "Installing slack-utils to $InstallDir..."
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -LiteralPath $binaryPath -Destination (Join-Path $InstallDir "slack-utils.exe") -Force

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not $NoPathUpdate -and -not (Test-PathContains $userPath $InstallDir)) {
        $newUserPath = if ([string]::IsNullOrWhiteSpace($userPath)) { $InstallDir } else { "$userPath;$InstallDir" }
        [Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
        $env:Path = "$env:Path;$InstallDir"
        Write-Host "Added $InstallDir to your user PATH. Restart open terminals to pick it up."
    }

    Write-Host "Installed slack-utils at $(Join-Path $InstallDir 'slack-utils.exe')"
} finally {
    if (Test-Path -LiteralPath $tmp) {
        Remove-Item -LiteralPath $tmp -Recurse -Force
    }
}
