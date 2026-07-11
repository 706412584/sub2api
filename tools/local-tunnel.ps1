#Requires -Version 5.1
<#
.SYNOPSIS
  Local Sub2API (18080) + Cloudflare Tunnel status check / one-click start.

.DESCRIPTION
  Runs on the Windows host, NOT inside the 18080 container:
  - A container cannot reliably manage Docker Desktop or the Windows cloudflared service
  - Tunnel should stay on the host and forward to 127.0.0.1:18080

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File tools/local-tunnel.ps1 status
  powershell -ExecutionPolicy Bypass -File tools/local-tunnel.ps1 up
  powershell -ExecutionPolicy Bypass -File tools/local-tunnel.ps1 start
#>

param(
    [Parameter(Position = 0)]
    [ValidateSet('status', 'up', 'start', 'stop-tunnel', 'help')]
    [string]$Command = 'status',

    [string]$LocalHealthUrl = 'http://127.0.0.1:18080/health',
    [string]$PublicHealthUrl = 'https://sub2api.smliewoker.online/health',
    [string]$CloudflaredService = 'cloudflared',
    [int]$WaitSeconds = 60
)

$ErrorActionPreference = 'Continue'

$Containers = @(
    'sub2api-postgres-dev',
    'sub2api-redis-dev',
    'sub2api-dev'
)

function Write-Ok([string]$Msg) { Write-Host "[OK]  $Msg" -ForegroundColor Green }
function Write-WarnLine([string]$Msg) { Write-Host "[!!]  $Msg" -ForegroundColor Yellow }
function Write-Fail([string]$Msg) { Write-Host "[XX]  $Msg" -ForegroundColor Red }
function Write-Info([string]$Msg) { Write-Host "[..]  $Msg" -ForegroundColor Cyan }

function Test-CommandExists([string]$Name) {
    return [bool](Get-Command $Name -ErrorAction SilentlyContinue)
}

function Get-DockerDaemonOk {
    if (-not (Test-CommandExists 'docker')) {
        return $false
    }
    docker info --format '{{.ServerVersion}}' 2>$null | Out-Null
    return ($LASTEXITCODE -eq 0)
}

function Get-ContainerState([string]$Name) {
    $raw = docker inspect -f '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}|{{.State.Running}}' $Name 2>$null
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($raw)) {
        return [pscustomobject]@{
            Exists  = $false
            Status  = 'missing'
            Health  = 'n/a'
            Running = $false
        }
    }
    $parts = $raw.Trim().Split('|')
    return [pscustomobject]@{
        Exists  = $true
        Status  = $parts[0]
        Health  = $parts[1]
        Running = ($parts[2] -eq 'true')
    }
}

function Get-ServiceState([string]$Name) {
    try {
        $svc = Get-Service -Name $Name -ErrorAction Stop
        return [pscustomobject]@{
            Exists    = $true
            Status    = [string]$svc.Status
            StartType = [string]$svc.StartType
        }
    } catch {
        return [pscustomobject]@{
            Exists    = $false
            Status    = 'missing'
            StartType = 'n/a'
        }
    }
}

function Get-HttpCode([string]$Url, [int]$TimeoutSec = 8) {
    try {
        $resp = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec $TimeoutSec -ErrorAction Stop
        return [int]$resp.StatusCode
    } catch {
        if ($_.Exception.Response -and $_.Exception.Response.StatusCode) {
            return [int]$_.Exception.Response.StatusCode
        }
        return 0
    }
}

function Wait-HttpOk([string]$Url, [string]$Name, [int]$Seconds) {
    for ($i = 1; $i -le $Seconds; $i++) {
        $code = Get-HttpCode $Url
        if ($code -eq 200) {
            Write-Ok "$Name ready ($code) in ${i}s: $Url"
            return $true
        }
        if (($i % 5) -eq 0 -or $i -eq 1) {
            Write-Info "$Name waiting ${i}/${Seconds}s status=$code"
        }
        Start-Sleep -Seconds 1
    }
    Write-Fail "$Name not ready within ${Seconds}s: $Url"
    return $false
}

function Show-Status {
    $failed = 0

    Write-Host ''
    Write-Host '=== Docker ===' -ForegroundColor White
    if (-not (Test-CommandExists 'docker')) {
        Write-Fail 'docker command not found (install/start Docker Desktop first)'
        $failed++
    } elseif (-not (Get-DockerDaemonOk)) {
        Write-Fail 'Docker daemon unavailable (start Docker Desktop)'
        $failed++
    } else {
        Write-Ok 'Docker daemon available'
    }

    Write-Host ''
    Write-Host '=== Containers ===' -ForegroundColor White
    foreach ($name in $Containers) {
        $st = Get-ContainerState $name
        if (-not $st.Exists) {
            Write-Fail "$name missing"
            $failed++
            continue
        }
        if (-not $st.Running) {
            Write-Fail "$name not running (status=$($st.Status))"
            $failed++
            continue
        }
        if ($st.Health -eq 'unhealthy') {
            Write-WarnLine "$name running but unhealthy"
            $failed++
            continue
        }
        Write-Ok "$name running health=$($st.Health)"
    }

    Write-Host ''
    Write-Host '=== Local app ===' -ForegroundColor White
    $localCode = Get-HttpCode $LocalHealthUrl
    if ($localCode -eq 200) {
        Write-Ok "local health $localCode  $LocalHealthUrl"
    } else {
        Write-Fail "local health=$localCode  $LocalHealthUrl"
        $failed++
    }

    Write-Host ''
    Write-Host '=== cloudflared ===' -ForegroundColor White
    $svc = Get-ServiceState $CloudflaredService
    if (-not $svc.Exists) {
        Write-Fail "Windows service $CloudflaredService missing (install connector first)"
        $failed++
    } elseif ($svc.Status -ne 'Running') {
        Write-Fail "$CloudflaredService status=$($svc.Status) StartType=$($svc.StartType)"
        $failed++
    } else {
        Write-Ok "$CloudflaredService Running (StartType=$($svc.StartType))"
    }

    Write-Host ''
    Write-Host '=== Public tunnel ===' -ForegroundColor White
    $publicCode = Get-HttpCode $PublicHealthUrl 15
    if ($publicCode -eq 200) {
        Write-Ok "public health $publicCode  $PublicHealthUrl"
    } else {
        Write-Fail "public health=$publicCode  $PublicHealthUrl"
        $failed++
    }

    Write-Host ''
    if ($failed -eq 0) {
        Write-Ok 'all checks passed'
        return 0
    }
    Write-Fail "$failed check(s) failed. Run: powershell -ExecutionPolicy Bypass -File tools/local-tunnel.ps1 up"
    return 1
}

function Start-DockerDesktopIfNeeded {
    if (Get-DockerDaemonOk) {
        Write-Ok 'Docker already available'
        return $true
    }

    Write-Info 'starting Docker Desktop...'
    $candidates = @(
        "$env:ProgramFiles\Docker\Docker\Docker Desktop.exe",
        "${env:ProgramFiles(x86)}\Docker\Docker\Docker Desktop.exe",
        "$env:LOCALAPPDATA\Docker\Docker Desktop.exe"
    ) | Where-Object { $_ -and (Test-Path $_) }

    if (-not $candidates) {
        Write-Fail 'Docker Desktop executable not found'
        return $false
    }

    Start-Process -FilePath $candidates[0] | Out-Null
    for ($i = 1; $i -le 90; $i++) {
        if (Get-DockerDaemonOk) {
            Write-Ok "Docker Desktop ready (${i}s)"
            return $true
        }
        Start-Sleep -Seconds 2
    }
    Write-Fail 'Docker Desktop start timeout'
    return $false
}

function Start-Containers {
    if (-not (Start-DockerDesktopIfNeeded)) {
        return $false
    }

    foreach ($name in $Containers) {
        $st = Get-ContainerState $name
        if (-not $st.Exists) {
            Write-Fail "container $name does not exist; create it via your compose/dev flow first"
            return $false
        }
        if ($st.Running) {
            Write-Ok "$name already running"
            continue
        }
        Write-Info "starting $name ..."
        docker start $name | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-Fail "docker start $name failed"
            return $false
        }
    }
    return $true
}

function Start-CloudflaredService {
    $svc = Get-ServiceState $CloudflaredService
    if (-not $svc.Exists) {
        Write-Fail "service $CloudflaredService missing"
        return $false
    }
    if ($svc.Status -eq 'Running') {
        Write-Ok "$CloudflaredService already running"
        return $true
    }

    Write-Info "starting Windows service $CloudflaredService ..."
    try {
        Start-Service -Name $CloudflaredService -ErrorAction Stop
    } catch {
        Write-WarnLine "Start-Service failed, try sc start: $($_.Exception.Message)"
        sc.exe start $CloudflaredService | Out-Null
    }

    for ($i = 1; $i -le 20; $i++) {
        $now = Get-ServiceState $CloudflaredService
        if ($now.Status -eq 'Running') {
            Write-Ok "$CloudflaredService started"
            return $true
        }
        Start-Sleep -Seconds 1
    }
    Write-Fail "$CloudflaredService start failed/timeout"
    return $false
}

function Restart-CloudflaredService {
    $svc = Get-ServiceState $CloudflaredService
    if (-not $svc.Exists) {
        Write-Fail "service $CloudflaredService missing"
        return $false
    }

    Write-Info "restarting $CloudflaredService (service Running but public tunnel unhealthy) ..."
    try {
        Restart-Service -Name $CloudflaredService -Force -ErrorAction Stop
    } catch {
        Write-WarnLine "Restart-Service failed, try stop/start: $($_.Exception.Message)"
        sc.exe stop $CloudflaredService | Out-Null
        Start-Sleep -Seconds 2
        sc.exe start $CloudflaredService | Out-Null
    }

    for ($i = 1; $i -le 20; $i++) {
        $now = Get-ServiceState $CloudflaredService
        if ($now.Status -eq 'Running') {
            Write-Ok "$CloudflaredService restarted"
            return $true
        }
        Start-Sleep -Seconds 1
    }
    Write-Fail "$CloudflaredService restart failed/timeout"
    return $false
}

function Invoke-Up {
    $ok = $true
    if (-not (Start-Containers)) { $ok = $false }
    if (-not (Wait-HttpOk $LocalHealthUrl 'local app' $WaitSeconds)) { $ok = $false }
    if (-not (Start-CloudflaredService)) { $ok = $false }

    # If service is up but public still bad (stale tunnel / 530), bounce cloudflared once.
    $publicCode = Get-HttpCode $PublicHealthUrl 12
    if ($publicCode -ne 200) {
        Write-WarnLine "public health=$publicCode before wait; bounce cloudflared"
        if (-not (Restart-CloudflaredService)) { $ok = $false }
    }

    if (-not (Wait-HttpOk $PublicHealthUrl 'public tunnel' $WaitSeconds)) {
        # One more bounce + short rewait for stuck "Running but dead" cases.
        Write-WarnLine 'public still unhealthy; second cloudflared bounce'
        if (-not (Restart-CloudflaredService)) {
            $ok = $false
        } elseif (-not (Wait-HttpOk $PublicHealthUrl 'public tunnel' 30)) {
            $ok = $false
        }
    }

    Write-Host ''
    if ($ok -and ((Get-HttpCode $PublicHealthUrl 12) -eq 200)) {
        Write-Ok 'one-click start finished'
        return 0
    }
    Write-Fail 'one-click start incomplete; see failures above'
    return 1
}

function Stop-TunnelOnly {
    $svc = Get-ServiceState $CloudflaredService
    if (-not $svc.Exists) {
        Write-Fail "service $CloudflaredService missing"
        return 1
    }
    if ($svc.Status -ne 'Running') {
        Write-Ok "$CloudflaredService already stopped"
        return 0
    }
    Write-Info "stopping $CloudflaredService ..."
    try {
        Stop-Service -Name $CloudflaredService -Force -ErrorAction Stop
    } catch {
        sc.exe stop $CloudflaredService | Out-Null
    }
    Write-Ok 'tunnel service stop requested (18080 containers untouched)'
    return 0
}

function Show-Help {
    Write-Host 'Usage:'
    Write-Host '  powershell -ExecutionPolicy Bypass -File tools/local-tunnel.ps1 status'
    Write-Host '  powershell -ExecutionPolicy Bypass -File tools/local-tunnel.ps1 up'
    Write-Host '  powershell -ExecutionPolicy Bypass -File tools/local-tunnel.ps1 start'
    Write-Host '  powershell -ExecutionPolicy Bypass -File tools/local-tunnel.ps1 stop-tunnel'
    Write-Host ''
    Write-Host 'Checks:'
    Write-Host '  * Docker Desktop / daemon'
    Write-Host '  * containers: sub2api-postgres-dev, sub2api-redis-dev, sub2api-dev'
    Write-Host "  * local: $LocalHealthUrl"
    Write-Host "  * Windows service: $CloudflaredService"
    Write-Host "  * public: $PublicHealthUrl"
    Write-Host ''
    Write-Host 'Note:'
    Write-Host '  Do NOT put this logic inside the 18080 container.'
    Write-Host '  The container cannot manage host Docker or the Windows cloudflared service.'
    Write-Host '  Keep the tunnel on the host, forwarding HTTP to 127.0.0.1:18080.'
}

switch ($Command) {
    'help' { Show-Help; exit 0 }
    'status' { exit (Show-Status) }
    'up' { exit (Invoke-Up) }
    'start' { exit (Invoke-Up) }
    'stop-tunnel' { exit (Stop-TunnelOnly) }
    default { Show-Help; exit 1 }
}
