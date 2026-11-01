<#
.SYNOPSIS
  Installs, upgrades or removes the mcpgw gateway as a Windows service.

.DESCRIPTION
  Works as `.\install.ps1 -Version 1.2.3 -DatabaseUrl postgres://...` and as
  `irm https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.ps1 | iex`.
  When piped, every parameter can be given as an env var MCPGW_INSTALL_<NAME>
  (VERSION, BASE_URL, DATABASE_URL, INSTALL_POSTGRES, PUBLIC_URL, LISTEN_ADDR, ENGINE,
  ADMIN_EMAIL, ADMIN_PASSWORD, NON_INTERACTIVE, NO_SERVICE, UNINSTALL, PURGE).
  Must run elevated. Compatible with Windows PowerShell 5.1 and PowerShell 7.

.PARAMETER Version        Release to install, with or without leading "v" (default: latest).
.PARAMETER BaseUrl        Download root (default: GitHub releases) or a local directory.
.PARAMETER DatabaseUrl    Existing Postgres 18+ URL. Mutually exclusive with -InstallPostgres.
.PARAMETER InstallPostgres Install PostgreSQL 18 through winget (unattended).
.PARAMETER PublicUrl      MCPGW_PUBLIC_URL (default http://<listen address>).
.PARAMETER ListenAddr     MCPGW_LISTEN_ADDR (default 127.0.0.1:8787).
.PARAMETER Engine         Sandbox engine: qjs (default) or monty.
.PARAMETER AdminEmail     Creates the first admin user (needs a password).
.PARAMETER AdminPassword  SecureString; or env MCPGW_INSTALL_ADMIN_PASSWORD (plain text).
.PARAMETER NonInteractive Never prompt; fail when a required value is missing.
.PARAMETER NoService      Install files and config only; do not register or start the service.
.PARAMETER Uninstall      Remove the service, binaries and PATH entry (never the database).
.PARAMETER Purge          With -Uninstall: also remove %ProgramData%\mcpgw (config, logs).
#>
[CmdletBinding()]
param(
    [string]$Version,
    [string]$BaseUrl,
    [string]$DatabaseUrl,
    [switch]$InstallPostgres,
    [string]$PublicUrl,
    [string]$ListenAddr,
    [string]$Engine,
    [string]$AdminEmail,
    [securestring]$AdminPassword,
    [switch]$NonInteractive,
    [switch]$NoService,
    [switch]$Uninstall,
    [switch]$Purge
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$tls = [Net.SecurityProtocolType]::Tls12
if ([Enum]::GetNames([Net.SecurityProtocolType]) -contains 'Tls13') { $tls = $tls -bor [Net.SecurityProtocolType]::Tls13 }
[Net.ServicePointManager]::SecurityProtocol = $tls

$DefaultBaseUrl = 'https://github.com/lucasbrito-wdt/mcpgw/releases/download'
$ReleasesApi = 'https://api.github.com/repos/lucasbrito-wdt/mcpgw/releases/latest'
$ServiceName = 'mcpgw'
$ServiceAccount = 'NT SERVICE\mcpgw'
$InstallDir = Join-Path $env:ProgramFiles 'mcpgw'
$DataDir = Join-Path $env:ProgramData 'mcpgw'
$LogDir = Join-Path $DataDir 'logs'
$LogFile = Join-Path $LogDir 'mcpgw.log'
$EnvFile = Join-Path $DataDir 'mcpgw.env'
$PgSuperFile = Join-Path $DataDir 'pg-superuser.txt'
$PgServiceName = 'postgresql-x64-18'
# Unverified default; override with MCPGW_INSTALL_EDB_URL.
$EdbInstallerUrl = 'https://get.enterprisedb.com/postgresql/postgresql-18.0-1-windows-x64.exe'
$HealthTimeoutSec = 60

$scriptPath = $null
$psCommandPath = Get-Variable -Name PSCommandPath -ValueOnly -ErrorAction SilentlyContinue
if ($psCommandPath) { $scriptPath = $psCommandPath }

# ---------- helpers ----------

function Write-Step([string]$Message) { Write-Host "==> $Message" }

function Test-Truthy([string]$Value) { return ($Value -match '^(1|true|yes|y|on)$') }

function Resolve-Setting([string]$Current, [string]$EnvName, [string]$Default) {
    if ($Current) { return $Current }
    $fromEnv = [Environment]::GetEnvironmentVariable("MCPGW_INSTALL_$EnvName")
    if ($fromEnv) { return $fromEnv }
    return $Default
}

function Resolve-Switch([bool]$Current, [string]$EnvName) {
    return ($Current -or (Test-Truthy ([Environment]::GetEnvironmentVariable("MCPGW_INSTALL_$EnvName"))))
}

function ConvertTo-PlainText([securestring]$Secret) {
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secret)
    try { return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr) }
    finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr) }
}

function Test-IsAdmin {
    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-ElevatedCommand {
    if ($scriptPath) {
        $parts = foreach ($key in $PSBoundParameters.Keys) {
            if ($key -eq 'AdminPassword') { continue }
            $value = $PSBoundParameters[$key]
            if ($value -is [System.Management.Automation.SwitchParameter]) { "-$key" } else { "-$key '$value'" }
        }
        return "Start-Process powershell -Verb RunAs -ArgumentList '-NoProfile -ExecutionPolicy Bypass -File ""$scriptPath"" $($parts -join ' ')'"
    }
    return "Open PowerShell as Administrator and run: irm https://github.com/lucasbrito-wdt/mcpgw/releases/latest/download/install.ps1 | iex"
}

function New-RandomBase64([int]$ByteCount) {
    $bytes = New-Object byte[] $ByteCount
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return [Convert]::ToBase64String($bytes)
}

function New-RandomPassword([int]$Length) {
    $alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789'
    $chars = New-Object System.Text.StringBuilder
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    $buffer = New-Object byte[] 1
    try {
        while ($chars.Length -lt $Length) {
            $rng.GetBytes($buffer)
            # 252 = 36 * 7: reject higher bytes to avoid modulo bias.
            if ($buffer[0] -lt 252) { [void]$chars.Append($alphabet[$buffer[0] % $alphabet.Length]) }
        }
    } finally { $rng.Dispose() }
    return $chars.ToString()
}

# Runs a native command without PS5.1 turning stderr into terminating errors.
function Invoke-Native([string]$Exe, [string[]]$ArgList, [string]$StdIn, [hashtable]$ChildEnv) {
    $previousPreference = $ErrorActionPreference
    $previousEnv = @{}
    $ErrorActionPreference = 'Continue'
    if ($ChildEnv) {
        foreach ($name in $ChildEnv.Keys) {
            $previousEnv[$name] = [Environment]::GetEnvironmentVariable($name)
            [Environment]::SetEnvironmentVariable($name, $ChildEnv[$name])
        }
    }
    try {
        if ($StdIn) { $output = $StdIn | & $Exe @ArgList 2>&1 } else { $output = & $Exe @ArgList 2>&1 }
        $code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $previousPreference
        foreach ($name in $previousEnv.Keys) { [Environment]::SetEnvironmentVariable($name, $previousEnv[$name]) }
    }
    $text = (@($output) | ForEach-Object { "$_" }) -join "`n"
    return @{ ExitCode = $code; Output = $text.Trim() }
}

function Set-StrictAcl([string]$Path, [string[]]$ExtraGrants) {
    $grants = @('*S-1-5-18:(F)', '*S-1-5-32-544:(F)') + $ExtraGrants
    $arguments = @($Path, '/inheritance:r', '/grant:r') + $grants
    $result = Invoke-Native 'icacls.exe' $arguments
    if ($result.ExitCode -ne 0) { throw "icacls failed on ${Path}: $($result.Output)" }
}

function Write-SecretFile([string]$Path, [string]$Content, [string[]]$ExtraGrants) {
    # Create empty, lock the ACL, and only then write the secret.
    [IO.File]::WriteAllText($Path, '')
    Set-StrictAcl $Path $ExtraGrants
    [IO.File]::WriteAllText($Path, $Content, (New-Object Text.UTF8Encoding($false)))
}

function Read-EnvValue([string]$Path, [string]$Key) {
    if (-not (Test-Path -LiteralPath $Path)) { return $null }
    foreach ($line in [IO.File]::ReadAllLines($Path)) {
        if ($line -match "^\s*$Key\s*=\s*(.*?)\s*$") { return $Matches[1].Trim('"', "'") }
    }
    return $null
}

function Update-MachinePath([string]$Dir, [bool]$Add) {
    $key = [Microsoft.Win32.Registry]::LocalMachine.OpenSubKey('SYSTEM\CurrentControlSet\Control\Session Manager\Environment', $true)
    try {
        $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $entries = @($raw -split ';' | Where-Object { $_ })
        $kept = @($entries | Where-Object { $_.TrimEnd('\') -ne $Dir.TrimEnd('\') })
        $updated = if ($Add) { $kept + $Dir } else { $kept }
        if (($updated -join ';') -ne ($entries -join ';')) {
            $key.SetValue('Path', ($updated -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
        }
    } finally { $key.Dispose() }
    $env:Path = (@($env:Path -split ';' | Where-Object { $_ -and $_.TrimEnd('\') -ne $Dir.TrimEnd('\') }) + $(if ($Add) { $Dir })) -join ';'
}

function Test-ServiceExists([string]$Name) {
    return ($null -ne (Get-Service -Name $Name -ErrorAction SilentlyContinue))
}

function Stop-GatewayService {
    if (-not (Test-ServiceExists $ServiceName)) { return }
    $service = Get-Service -Name $ServiceName
    if ($service.Status -ne 'Stopped') {
        Write-Step 'Stopping service mcpgw'
        Stop-Service -Name $ServiceName -Force
        $service.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30))
    }
    Get-Process -Name 'montyrs-worker' -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "$InstallDir\*" } | Stop-Process -Force
}

# ---------- download ----------

function Resolve-ReleaseVersion([string]$Requested, [string]$Root) {
    if ($Requested -and $Requested -ne 'latest') { return $Requested.TrimStart('v') }
    if ($Root -ne $DefaultBaseUrl) { throw '-Version is required when -BaseUrl is not the default GitHub releases URL.' }
    $release = Invoke-RestMethod -UseBasicParsing -Uri $ReleasesApi -Headers @{ 'User-Agent' = 'mcpgw-installer' }
    return ([string]$release.tag_name).TrimStart('v')
}

# Fetches one release asset to $Dest from a URL root or a local directory (tries <root>\v<ver>\ then <root>\).
function Get-ReleaseAsset([string]$Root, [string]$Ver, [string]$Name, [string]$Dest) {
    if (Test-Path -LiteralPath $Root -PathType Container) {
        foreach ($candidate in @((Join-Path (Join-Path $Root "v$Ver") $Name), (Join-Path $Root $Name))) {
            if (Test-Path -LiteralPath $candidate) { Copy-Item -LiteralPath $candidate -Destination $Dest; return }
        }
        throw "$Name not found under $Root"
    }
    $url = "$($Root.TrimEnd('/'))/v$Ver/$Name"
    try { Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $Dest -Headers @{ 'User-Agent' = 'mcpgw-installer' } }
    catch { throw "download failed: $url ($($_.Exception.Message))" }
}

function Assert-Checksum([string]$ArchivePath, [string]$SumsPath) {
    $name = Split-Path -Leaf $ArchivePath
    $expected = $null
    foreach ($line in [IO.File]::ReadAllLines($SumsPath)) {
        if ($line -match '^([0-9a-fA-F]{64})\s+\*?(.+?)\s*$' -and $Matches[2] -eq $name) { $expected = $Matches[1] }
    }
    if (-not $expected) { throw "no SHA256SUMS entry for $name" }
    $actual = (Get-FileHash -LiteralPath $ArchivePath -Algorithm SHA256).Hash
    if ($actual -ne $expected.ToUpperInvariant()) { throw "SHA256 mismatch for $name (expected $expected, got $actual); aborting" }
    Write-Host "    sha256 ok: $name"
}

function Get-VerifiedRelease([string]$Root, [string]$Ver, [string]$WorkDir) {
    $archiveName = "mcpgw_${Ver}_windows_amd64.zip"
    $archivePath = Join-Path $WorkDir $archiveName
    $sumsPath = Join-Path $WorkDir 'SHA256SUMS'
    Write-Step "Downloading mcpgw $Ver"
    Get-ReleaseAsset $Root $Ver $archiveName $archivePath
    Get-ReleaseAsset $Root $Ver 'SHA256SUMS' $sumsPath
    Assert-Checksum $archivePath $sumsPath
    $extractDir = Join-Path $WorkDir 'extracted'
    Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDir -Force
    foreach ($required in 'mcpgw.exe', 'montyrs-worker.exe') {
        if (-not (Test-Path -LiteralPath (Join-Path $extractDir $required))) { throw "$required missing from archive" }
    }
    return $extractDir
}

# ---------- install ----------

# Copy to .new, move the current file to .old, then rename: the swap itself is two renames.
function Install-Binaries([string]$SourceDir) {
    Write-Step "Installing binaries to $InstallDir"
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    foreach ($file in 'mcpgw.exe', 'montyrs-worker.exe', 'LICENSE', 'NOTICE', 'README.md') {
        $source = Join-Path $SourceDir $file
        if (-not (Test-Path -LiteralPath $source)) { continue }
        $target = Join-Path $InstallDir $file
        Copy-Item -LiteralPath $source -Destination "$target.new" -Force
        if (Test-Path -LiteralPath $target) { Move-Item -LiteralPath $target -Destination "$target.old" -Force }
        Move-Item -LiteralPath "$target.new" -Destination $target -Force
    }
    # AppContainer (S-1-15-2-1) must read the directory and run the sandbox worker.
    foreach ($path in @($InstallDir, (Join-Path $InstallDir 'montyrs-worker.exe'))) {
        $result = Invoke-Native 'icacls.exe' @($path, '/grant', '*S-1-15-2-1:(RX)')
        if ($result.ExitCode -ne 0) { throw "icacls failed on ${path}: $($result.Output)" }
    }
    Update-MachinePath $InstallDir $true
}

function Remove-OldBinaries {
    Get-ChildItem -LiteralPath $InstallDir -Filter '*.old' -ErrorAction SilentlyContinue | Remove-Item -Force
}

function Register-GatewayService {
    $binaryPath = '"{0}" serve --env-file "{1}"' -f (Join-Path $InstallDir 'mcpgw.exe'), $EnvFile
    if (-not (Test-ServiceExists $ServiceName)) {
        Write-Step 'Registering service mcpgw'
        New-Service -Name $ServiceName -BinaryPathName $binaryPath -DisplayName 'MCP Gateway (mcpgw)' `
            -Description 'mcpgw: MCP gateway with sandboxed code mode' -StartupType Automatic | Out-Null
    } else {
        # Registry write avoids sc.exe/PS5.1 quoting problems with embedded quotes.
        Set-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName" -Name ImagePath -Value $binaryPath
        Set-Service -Name $ServiceName -StartupType Automatic
    }
    $steps = @(
        @('config', $ServiceName, 'obj=', $ServiceAccount),
        @('failure', $ServiceName, 'reset=', '86400', 'actions=', 'restart/5000/restart/5000/restart/30000')
    )
    foreach ($step in $steps) {
        $result = Invoke-Native 'sc.exe' $step
        if ($result.ExitCode -ne 0) { throw "sc.exe $($step[0]) failed: $($result.Output)" }
    }
}

function Initialize-DataDirs {
    New-Item -ItemType Directory -Force -Path $DataDir, $LogDir | Out-Null
    $grantee = if (Test-ServiceExists $ServiceName) { $ServiceAccount } else { $null }
    if ($grantee) {
        $result = Invoke-Native 'icacls.exe' @($LogDir, '/grant', "${grantee}:(OI)(CI)(M)")
        if ($result.ExitCode -ne 0) { throw "icacls failed on ${LogDir}: $($result.Output)" }
    }
}

# ---------- postgres ----------

function Find-Psql {
    $local = Join-Path $InstallDir 'psql.exe'
    if (Test-Path -LiteralPath $local) { return $local }
    $onPath = Get-Command 'psql.exe' -ErrorAction SilentlyContinue
    if ($onPath) { return $onPath.Source }
    $pgBin = Join-Path $env:ProgramFiles 'PostgreSQL\18\bin\psql.exe'
    if (Test-Path -LiteralPath $pgBin) { return $pgBin }
    return $null
}

function Invoke-Psql([string[]]$ArgList, [string]$Sql, [string]$SuperPassword) {
    $psql = Find-Psql
    if (-not $psql) { throw 'psql.exe not found (expected in the install dir, on PATH or in Program Files\PostgreSQL\18\bin).' }
    $envVars = @{}
    if ($SuperPassword) { $envVars['PGPASSWORD'] = $SuperPassword }
    return Invoke-Native $psql (@('-X', '-v', 'ON_ERROR_STOP=1') + $ArgList) $Sql $envVars
}

function Assert-ExistingPostgres([string]$Url) {
    if (-not (Find-Psql)) {
        Write-Warning 'psql.exe not found: skipping connection and version check; migrate will fail if the URL is wrong.'
        return
    }
    $result = Invoke-Psql @($Url, '-tA', '-c', 'SHOW server_version_num') $null $null
    if ($result.ExitCode -ne 0) { throw 'cannot connect to the given database URL (check host, credentials and network).' }
    if ([int]$result.Output -lt 180000) { throw "PostgreSQL 18+ is required (server_version_num=$($result.Output))." }
}

function Test-PortListening([int]$Port) {
    $client = New-Object Net.Sockets.TcpClient
    try {
        $pending = $client.BeginConnect('127.0.0.1', $Port, $null, $null)
        return ($pending.AsyncWaitHandle.WaitOne(2000) -and $client.Connected)
    } catch { return $false } finally { $client.Close() }
}

# Runs the EDB installer: through winget when present, else downloads it directly.
function Invoke-PostgresInstaller([string]$Override) {
    if (Get-Command 'winget.exe' -ErrorAction SilentlyContinue) {
        $result = Invoke-Native 'winget.exe' @('install', '--id', 'PostgreSQL.PostgreSQL.18', '-e', '--silent',
            '--accept-package-agreements', '--accept-source-agreements', '--override', $Override)
        if ($result.ExitCode -eq 0) { return }
        Write-Warning 'winget failed; falling back to the EDB installer.'
    }
    $url = Resolve-Setting '' 'EDB_URL' $EdbInstallerUrl
    $installer = Join-Path ([IO.Path]::GetTempPath()) 'mcpgw-postgresql-installer.exe'
    try {
        try { Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $installer }
        catch { throw "could not download the EDB installer from $url ($($_.Exception.Message)). Install PostgreSQL 18 manually and re-run with -DatabaseUrl, or set MCPGW_INSTALL_EDB_URL." }
        $process = Start-Process -FilePath $installer -ArgumentList $Override -Wait -PassThru
        if ($process.ExitCode -ne 0) { throw "EDB installer exited with code $($process.ExitCode)." }
    } finally { Remove-Item -LiteralPath $installer -Force -ErrorAction SilentlyContinue }
}

function Install-PostgresWithWinget {
    if (Test-ServiceExists $PgServiceName) {
        if (Test-Path -LiteralPath $PgSuperFile) { return (Get-Content -LiteralPath $PgSuperFile -Raw).Trim() }
        throw "service $PgServiceName already exists but its superuser password is unknown to this installer; re-run with -DatabaseUrl."
    }
    if (Test-PortListening 5432) {
        throw 'port 5432 is already in use by another PostgreSQL/server; refusing to install a second one. Re-run with -DatabaseUrl pointing at it.'
    }
    $superPassword = New-RandomPassword 24
    Write-Step 'Installing PostgreSQL 18 (this takes a few minutes)'
    Invoke-PostgresInstaller "--mode unattended --superpassword $superPassword --servicename $PgServiceName --serverport 5432"
    New-Item -ItemType Directory -Force -Path $DataDir | Out-Null
    Write-SecretFile $PgSuperFile $superPassword @()
    return $superPassword
}

function Wait-PostgresReady([string]$SuperPassword) {
    $deadline = (Get-Date).AddSeconds(60)
    while ((Get-Date) -lt $deadline) {
        $result = Invoke-Psql @('-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-tA', '-c', 'SELECT 1') $null $SuperPassword
        if ($result.ExitCode -eq 0) { return }
        Start-Sleep -Seconds 2
    }
    throw 'PostgreSQL did not accept connections within 60s.'
}

# Creates (or re-passwords) role mcpgw and the mcpgw database; returns the connection URL.
function New-GatewayDatabase([string]$SuperPassword) {
    Wait-PostgresReady $SuperPassword
    $appPassword = New-RandomPassword 32
    $base = @('-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres')
    $roleExists = (Invoke-Psql ($base + @('-tA', '-c', "SELECT 1 FROM pg_roles WHERE rolname='mcpgw'")) $null $SuperPassword).Output -eq '1'
    $verb = if ($roleExists) { 'ALTER' } else { 'CREATE' }
    $role = Invoke-Psql ($base + @('-f', '-')) "$verb ROLE mcpgw LOGIN PASSWORD '$appPassword';" $SuperPassword
    if ($role.ExitCode -ne 0) { throw "creating role mcpgw failed: $($role.Output)" }
    $dbExists = (Invoke-Psql ($base + @('-tA', '-c', "SELECT 1 FROM pg_database WHERE datname='mcpgw'")) $null $SuperPassword).Output -eq '1'
    if (-not $dbExists) {
        $db = Invoke-Psql ($base + @('-c', 'CREATE DATABASE mcpgw OWNER mcpgw')) $null $SuperPassword
        if ($db.ExitCode -ne 0) { throw "creating database mcpgw failed: $($db.Output)" }
    }
    return "postgres://mcpgw:$appPassword@127.0.0.1:5432/mcpgw"
}

# ---------- config ----------

function Resolve-DatabaseUrl($Cfg) {
    if ($Cfg.DatabaseUrl) { Assert-ExistingPostgres $Cfg.DatabaseUrl; return $Cfg.DatabaseUrl }
    if (-not $Cfg.InstallPostgres) {
        if ($Cfg.NonInteractive) { throw 'pass -DatabaseUrl <postgres url> or -InstallPostgres (non-interactive mode).' }
        $answer = Read-Host 'PostgreSQL 18+: [1] install it now via winget, [2] use an existing database URL (1/2)'
        if ($answer -eq '2') {
            $Cfg.DatabaseUrl = Read-Host 'Database URL (postgres://user:pass@host:5432/db)'
            if (-not $Cfg.DatabaseUrl) { throw 'empty database URL.' }
            Assert-ExistingPostgres $Cfg.DatabaseUrl
            return $Cfg.DatabaseUrl
        }
    }
    $superPassword = Install-PostgresWithWinget
    return New-GatewayDatabase $superPassword
}

function Write-GatewayConfig($Cfg) {
    $databaseUrl = Resolve-DatabaseUrl $Cfg
    $publicUrl = if ($Cfg.PublicUrl) { $Cfg.PublicUrl } else { "http://$($Cfg.ListenAddr)" }
    $lines = @(
        '# mcpgw configuration. Contains secrets: keep this file private.',
        "MCPGW_DATABASE_URL=""$databaseUrl""",
        "MCPGW_LISTEN_ADDR=""$($Cfg.ListenAddr)""",
        "MCPGW_PUBLIC_URL=""$publicUrl""",
        "MCPGW_MASTER_KEY=""$(New-RandomBase64 32)""",
        "MCPGW_SANDBOX_ENGINE=""$($Cfg.Engine)"""
    )
    $grants = @()
    if (Test-ServiceExists $ServiceName) { $grants = @("${ServiceAccount}:(R)") }
    Write-SecretFile $EnvFile (($lines -join "`r`n") + "`r`n") $grants
    Write-Host "    config written: $EnvFile"
}

function Sync-ConfigAcl {
    # Upgrade path: make sure the service account can still read an existing config.
    if (Test-ServiceExists $ServiceName) { Set-StrictAcl $EnvFile @("${ServiceAccount}:(R)") }
}

# ---------- run ----------

function Invoke-Gateway([string[]]$ArgList, [hashtable]$ChildEnv) {
    $combined = @{ MCPGW_ENV_FILE = $EnvFile }
    if ($ChildEnv) { foreach ($key in $ChildEnv.Keys) { $combined[$key] = $ChildEnv[$key] } }
    return Invoke-Native (Join-Path $InstallDir 'mcpgw.exe') $ArgList $null $combined
}

function Invoke-Migrations {
    Write-Step 'Running database migrations'
    $result = Invoke-Gateway @('migrate', '--env-file', $EnvFile) $null
    if ($result.ExitCode -ne 0) { throw "mcpgw migrate failed:`n$($result.Output)" }
}

function New-AdminUser($Cfg) {
    if (-not $Cfg.AdminEmail) { return }
    if (-not $Cfg.AdminPassword) { Write-Warning '-AdminEmail given without a password; skipping admin creation.'; return }
    Write-Step "Creating admin user $($Cfg.AdminEmail)"
    $result = Invoke-Gateway @('admin', 'user', 'create', '--email', $Cfg.AdminEmail, '--admin') @{ MCPGW_ADMIN_PASSWORD = $Cfg.AdminPassword }
    if ($result.ExitCode -eq 0) { return }
    if ($result.Output -match 'already exists') { Write-Warning 'admin user already exists; left unchanged.'; return }
    throw "creating the admin user failed:`n$($result.Output)"
}

function Get-HealthUrl {
    $listen = Read-EnvValue $EnvFile 'MCPGW_LISTEN_ADDR'
    if (-not $listen) { $listen = '127.0.0.1:8787' }
    $port = $listen.Substring($listen.LastIndexOf(':') + 1)
    return "http://127.0.0.1:$port/healthz"
}

function Show-LogTail {
    if (Test-Path -LiteralPath $LogFile) {
        Write-Host "--- last lines of $LogFile ---"
        Get-Content -LiteralPath $LogFile -Tail 30 | ForEach-Object { Write-Host $_ }
    } else {
        Write-Host "No log file at $LogFile yet; check Event Viewer (Windows Logs > Application)."
    }
}

function Start-GatewayAndWait {
    Write-Step 'Starting service mcpgw'
    Start-Service -Name $ServiceName
    $url = Get-HealthUrl
    $deadline = (Get-Date).AddSeconds($HealthTimeoutSec)
    while ((Get-Date) -lt $deadline) {
        try {
            if ((Invoke-WebRequest -UseBasicParsing -Uri $url -TimeoutSec 3).StatusCode -eq 200) { return }
        } catch { Write-Verbose "health probe: $($_.Exception.Message)" }
        if ((Get-Service -Name $ServiceName).Status -eq 'Stopped') { break }
        Start-Sleep -Seconds 2
    }
    Show-LogTail
    throw "service did not become healthy at $url within ${HealthTimeoutSec}s. Previous binaries (if any) are kept as *.old in $InstallDir."
}

function Show-Summary($Cfg, [string]$Ver) {
    $publicUrl = Read-EnvValue $EnvFile 'MCPGW_PUBLIC_URL'
    Write-Host ''
    Write-Host "mcpgw $Ver installed."
    if ($publicUrl) { Write-Host "  URL:       $publicUrl" }
    Write-Host "  Config:    $EnvFile (secrets inside; not shown)"
    Write-Host "  Logs:      Get-Content '$LogFile' -Wait"
    Write-Host "  Service:   Get-Service $ServiceName"
    Write-Host '  Uninstall: re-run install.ps1 with -Uninstall (add -Purge to also remove config; the database is never dropped)'
    if (Test-Path -LiteralPath $PgSuperFile) { Write-Host "  Postgres superuser password kept (Administrators/SYSTEM only) in $PgSuperFile" }
}

function Invoke-Install($Cfg) {
    $workDir = Join-Path ([IO.Path]::GetTempPath()) ("mcpgw-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $workDir | Out-Null
    try {
        $ver = Resolve-ReleaseVersion $Cfg.Version $Cfg.BaseUrl
        $extractDir = Get-VerifiedRelease $Cfg.BaseUrl $ver $workDir
        Stop-GatewayService
        Install-Binaries $extractDir
    } finally { Remove-Item -LiteralPath $workDir -Recurse -Force -ErrorAction SilentlyContinue }

    if (-not $Cfg.NoService) { Register-GatewayService }
    Initialize-DataDirs
    if (Test-Path -LiteralPath $EnvFile) { Write-Step 'Keeping existing config'; Sync-ConfigAcl } else { Write-Step 'Writing config'; Write-GatewayConfig $Cfg }
    Invoke-Migrations
    New-AdminUser $Cfg
    if ($Cfg.NoService) { Write-Host "Service skipped (-NoService). Run: mcpgw serve --env-file `"$EnvFile`""; return }
    if ($Cfg.DatabaseUrl -eq $null -and (Test-Path -LiteralPath $PgSuperFile) -and (Test-ServiceExists $PgServiceName)) {
        [void](Invoke-Native 'sc.exe' @('config', $ServiceName, 'depend=', $PgServiceName))
    }
    Start-GatewayAndWait
    Remove-OldBinaries
    Show-Summary $Cfg $ver
}

function Invoke-Uninstall($Cfg) {
    Write-Step 'Uninstalling mcpgw'
    Stop-GatewayService
    if (Test-ServiceExists $ServiceName) {
        $result = Invoke-Native 'sc.exe' @('delete', $ServiceName)
        if ($result.ExitCode -ne 0) { throw "sc.exe delete failed: $($result.Output)" }
    }
    if (Test-Path -LiteralPath $InstallDir) { Remove-Item -LiteralPath $InstallDir -Recurse -Force }
    Update-MachinePath $InstallDir $false
    if ($Cfg.Purge) { Write-Warning "Purging $DataDir deletes MCPGW_MASTER_KEY: the data in the database can no longer be decrypted." }
    if ($Cfg.Purge -and (Test-Path -LiteralPath $DataDir)) { Remove-Item -LiteralPath $DataDir -Recurse -Force }
    Write-Host 'mcpgw removed. The PostgreSQL database and server were not touched.'
    if (-not $Cfg.Purge) { Write-Host "Config and logs kept in $DataDir (use -Purge to remove)." }
}

function Resolve-Config {
    $password = $null
    if ($AdminPassword) { $password = ConvertTo-PlainText $AdminPassword }
    else { $password = [Environment]::GetEnvironmentVariable('MCPGW_INSTALL_ADMIN_PASSWORD') }
    $cfg = @{
        Version         = Resolve-Setting $Version 'VERSION' 'latest'
        BaseUrl         = Resolve-Setting $BaseUrl 'BASE_URL' $DefaultBaseUrl
        DatabaseUrl     = Resolve-Setting $DatabaseUrl 'DATABASE_URL' ''
        PublicUrl       = Resolve-Setting $PublicUrl 'PUBLIC_URL' ''
        ListenAddr      = Resolve-Setting $ListenAddr 'LISTEN_ADDR' '127.0.0.1:8787'
        Engine          = Resolve-Setting $Engine 'ENGINE' 'qjs'
        AdminEmail      = Resolve-Setting $AdminEmail 'ADMIN_EMAIL' ''
        AdminPassword   = $password
        InstallPostgres = Resolve-Switch $InstallPostgres.IsPresent 'INSTALL_POSTGRES'
        NonInteractive  = Resolve-Switch $NonInteractive.IsPresent 'NON_INTERACTIVE'
        NoService       = Resolve-Switch $NoService.IsPresent 'NO_SERVICE'
        Uninstall       = Resolve-Switch $Uninstall.IsPresent 'UNINSTALL'
        Purge           = Resolve-Switch $Purge.IsPresent 'PURGE'
    }
    if ($cfg.Engine -notin @('qjs', 'monty')) { throw "-Engine must be qjs or monty (got '$($cfg.Engine)')." }
    if ($cfg.DatabaseUrl -and $cfg.InstallPostgres) { throw '-DatabaseUrl and -InstallPostgres are mutually exclusive.' }
    return $cfg
}

function Invoke-Main {
    $cfg = Resolve-Config
    if (-not (Test-IsAdmin)) {
        Write-Host 'This installer must run as Administrator. Run:'
        Write-Host "  $(Get-ElevatedCommand)"
        throw 'not running elevated.'
    }
    if (-not [Environment]::Is64BitOperatingSystem) { throw 'a 64-bit Windows is required.' }
    if ($cfg.Uninstall) { Invoke-Uninstall $cfg } else { Invoke-Install $cfg }
}

try {
    Invoke-Main
} catch {
    Write-Host "ERROR: $($_.Exception.Message)" -ForegroundColor Red
    if ($scriptPath) { exit 1 }
    $global:LASTEXITCODE = 1
}
