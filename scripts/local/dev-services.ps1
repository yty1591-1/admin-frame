# 本地开发：Windows 原生 PowerShell 编排（start|stop|restart|status|install）。
# 日常联调用本脚本；破坏性冒烟（TRUNCATE 密钥表）见 smoke-start-all.ps1。
#
# 用法：
#   pwsh -File scripts/local/dev-services.ps1 start
#   pwsh -File scripts/local/dev-services.ps1 stop
#   pwsh -File scripts/local/dev-services.ps1 restart
#   pwsh -File scripts/local/dev-services.ps1 status
#   pwsh -File scripts/local/dev-services.ps1 install
#   pwsh -File scripts/local/dev-services.ps1 start -NoBuild -Migrate
#
# 环境变量文件默认 scripts/local/.env（从 env.example 复制）。
# 启动流程统一为：config-api 就绪 → 安装向导（begin/apply/complete）→ 其余进程（ADR-0018）。

[CmdletBinding()]
param(
  [Parameter(Position = 0, Mandatory = $true)]
  [ValidateSet("start", "stop", "restart", "status", "install")]
  [string]$Action,

  [switch]$NoBuild,
  [switch]$SkipHealth,
  [switch]$Migrate,
  [string]$EnvFile = ""
)

$ErrorActionPreference = "Stop"
$Root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
Set-Location $Root

$BinDir = Join-Path $Root "bin"
$RuntimeDir = Join-Path $BinDir "run"
$PidDir = Join-Path $RuntimeDir "pids"
$LogDir = Join-Path $RuntimeDir "logs"
if (-not $EnvFile) { $EnvFile = Join-Path $Root "scripts\local\.env" }

$Roles = @("config-api", "worker", "admin-api", "biz-api", "c-api")
$Ports = @{
  "config-api" = 8085
  "admin-api"  = 8081
  "biz-api"    = 8082
  "c-api"      = 8083
  "worker"     = 8090
}

function Identity-For([string]$Role) { return $Role }

function Write-Log([string]$Message) { Write-Host $Message }
function Write-Warn([string]$Message) { Write-Host "WARN: $Message" -ForegroundColor Yellow }
function Die([string]$Message) { Write-Host "ERROR: $Message" -ForegroundColor Red; exit 1 }

function Import-DotEnv([string]$Path) {
  if (-not (Test-Path $Path)) {
    Die "缺少 $Path；请先：Copy-Item scripts\local\env.example scripts\local\.env"
  }
  Get-Content -Path $Path | ForEach-Object {
    $line = $_.Trim()
    if (-not $line -or $line.StartsWith("#")) { return }
    $idx = $line.IndexOf("=")
    if ($idx -lt 1) { return }
    $key = $line.Substring(0, $idx).Trim()
    $value = $line.Substring($idx + 1)
    if (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'"))) {
      $value = $value.Substring(1, $value.Length - 2)
    }
    Set-Item -Path "Env:$key" -Value $value
  }
  if (-not $env:CONFIG_API_URL) { $env:CONFIG_API_URL = "http://127.0.0.1:8085" }
  if (-not $env:CONFIGCENTER_ENV) { $env:CONFIGCENTER_ENV = "dev" }
  if (-not $env:CONFIGCENTER_CACHE_DIR) { $env:CONFIGCENTER_CACHE_DIR = "var\configcenter-cache" }
  if (-not $env:CONFIGCENTER_KEK_DRIVER) { $env:CONFIGCENTER_KEK_DRIVER = "local" }
  # 安装向导凭据：dev 缺省值仅用于本机自动安装（ADR-0018 统一安装流程）。
  if (-not $env:CONFIG_INSTALL_SETUP_TOKEN) { $env:CONFIG_INSTALL_SETUP_TOKEN = "local-dev-install-setup-token-0123456789abcdef" }
  New-Item -ItemType Directory -Force -Path $env:CONFIGCENTER_CACHE_DIR | Out-Null
  Write-Log "已加载环境变量：$Path"
}

function Exe-Path([string]$Role) {
  $exe = Join-Path $BinDir "$Role.exe"
  if (Test-Path $exe) { return $exe }
  return (Join-Path $BinDir $Role)
}

function Build-All {
  New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
  foreach ($role in $Roles) {
    Write-Log "构建 $role ..."
    & go build -ldflags "-s -w" -o (Join-Path $BinDir "$role.exe") "./cmd/$role"
    if ($LASTEXITCODE -ne 0) { Die "build $role failed" }
  }
}

function Read-Pid([string]$Role) {
  $f = Join-Path $PidDir "$Role.pid"
  if (-not (Test-Path $f)) { return $null }
  $pidText = (Get-Content $f -ErrorAction SilentlyContinue | Select-Object -First 1)
  if (-not $pidText) { return $null }
  $procId = 0
  if (-not [int]::TryParse($pidText.Trim(), [ref]$procId)) { return $null }
  $p = Get-Process -Id $procId -ErrorAction SilentlyContinue
  if (-not $p -or $p.HasExited) { return $null }
  return $procId
}

function Wait-Http([string]$Url, [int]$Seconds = 90) {
  $deadline = (Get-Date).AddSeconds($Seconds)
  while ((Get-Date) -lt $deadline) {
    try {
      $r = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2
      if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 300) { return $true }
    } catch {}
    Start-Sleep -Milliseconds 400
  }
  return $false
}

function Start-Role([string]$Role) {
  $exe = Exe-Path $Role
  if (-not (Test-Path $exe)) { Die "二进制不存在：$exe" }
  $existing = Read-Pid $Role
  if ($existing) {
    Write-Warn "$Role 已在运行（pid=$existing），跳过"
    return
  }
  $identity = Identity-For $Role
  $env:CONFIG_SERVICE_IDENTITY = $identity
  $log = Join-Path $LogDir "$Role.log"
  $err = Join-Path $LogDir "$Role.err.log"
  "=== $Role start $(Get-Date -Format o) identity=$identity ===" | Add-Content -Path $log
  $proc = Start-Process -FilePath $exe -WorkingDirectory $Root `
    -RedirectStandardOutput $log -RedirectStandardError $err -PassThru -WindowStyle Hidden
  Set-Content -Path (Join-Path $PidDir "$Role.pid") -Value $proc.Id -NoNewline
  Start-Sleep -Milliseconds 800
  if ($proc.HasExited) {
    if (Test-Path $err) { Get-Content $err -Tail 40 | Write-Host }
    Die "$Role 启动即退出 code=$($proc.ExitCode)"
  }
  Write-Log "$Role 已启动（pid=$($proc.Id), identity=$identity）"
}

function Stop-Role([string]$Role) {
  $procId = Read-Pid $Role
  $pidFile = Join-Path $PidDir "$Role.pid"
  if (-not $procId) {
    Remove-Item $pidFile -ErrorAction SilentlyContinue
    return
  }
  Write-Log "停止 $Role（pid=$procId）..."
  Stop-Process -Id $procId -Force -ErrorAction SilentlyContinue
  Remove-Item $pidFile -ErrorAction SilentlyContinue
}

function Invoke-Migrate {
  Write-Log "执行 migrate up ..."
  $mig = Join-Path $BinDir "migrate.exe"
  if (-not (Test-Path $mig)) {
    & go build -ldflags "-s -w" -o $mig ./cmd/migrate
    if ($LASTEXITCODE -ne 0) { Die "build migrate failed" }
  }
  & $mig -dir migrations -action up
  if ($LASTEXITCODE -ne 0) { Die "migrate up failed" }
}

function Get-InstallState {
  try {
    return Invoke-RestMethod -Uri "http://127.0.0.1:$($Ports['config-api'])/internal/v1/install/status" -TimeoutSec 5
  } catch {
    return $null
  }
}

function Invoke-Install {
  if (-not $env:CONFIG_INSTALL_SETUP_TOKEN) {
    Die "CONFIG_INSTALL_SETUP_TOKEN 未设置；见 scripts/local/env.example"
  }
  $state = Get-InstallState
  if ($state -and $state.data.status -eq "ready") {
    Write-Log "安装态已 ready，跳过安装向导"
    return
  }
  $owner = "local-dev"
  if ($state -and $state.data.owner) { $owner = $state.data.owner }
  $base = "http://127.0.0.1:$($Ports['config-api'])/internal/v1/install"
  $hdr = @{ "X-Config-Install-Token" = $env:CONFIG_INSTALL_SETUP_TOKEN }
  Write-Log "安装向导：begin → apply → complete（env=$($env:CONFIGCENTER_ENV)，owner=$owner）"
  $begin = @{ owner = $owner } | ConvertTo-Json -Compress
  $apply = @{ owner = $owner; overrides = @{} } | ConvertTo-Json -Compress
  $complete = @{ owner = $owner } | ConvertTo-Json -Compress
  try {
    Invoke-RestMethod -Method Post -Uri "$base/begin" -Headers $hdr -ContentType "application/json" -Body $begin | Out-Null
    Invoke-RestMethod -Method Post -Uri "$base/apply" -Headers $hdr -ContentType "application/json" -Body $apply | Out-Null
    Invoke-RestMethod -Method Post -Uri "$base/complete" -Headers $hdr -ContentType "application/json" -Body $complete | Out-Null
  } catch {
    Die "安装向导失败：$($_.Exception.Message)"
  }
  $state = Get-InstallState
  if (-not $state -or $state.data.status -ne "ready") {
    Die "安装后状态不是 ready：$($state | ConvertTo-Json -Depth 5)"
  }
  Write-Log "安装完成（status=ready）"
}

function Do-Start {
  New-Item -ItemType Directory -Force -Path $PidDir, $LogDir | Out-Null
  Import-DotEnv $EnvFile
  if (-not $NoBuild) { Build-All }
  if ($Migrate) { Invoke-Migrate }

  Start-Role "config-api"
  if (-not $SkipHealth) {
    if (-not (Wait-Http "http://127.0.0.1:$($Ports['config-api'])/readyz" 90)) {
      Get-Content (Join-Path $LogDir "config-api.err.log") -Tail 40 -ErrorAction SilentlyContinue
      Die "config-api 未就绪"
    }
    Write-Log "config-api ready"
    Invoke-Install
  }

  foreach ($role in @("worker", "admin-api", "biz-api", "c-api")) {
    Start-Role $role
    if ($role -eq "worker") { Start-Sleep -Seconds 1 }
  }

  if (-not $SkipHealth) {
    foreach ($role in $Roles) {
      $port = $Ports[$role]
      if (-not (Wait-Http "http://127.0.0.1:$port/readyz" 90)) {
        Get-Content (Join-Path $LogDir "$role.err.log") -Tail 40 -ErrorAction SilentlyContinue
        Die "$role /readyz 超时"
      }
    }
  }
  Write-Log "启动完成。日志：$LogDir"
}

function Do-Stop {
  foreach ($role in @("c-api", "biz-api", "admin-api", "worker", "config-api")) {
    Stop-Role $role
  }
  Write-Log "全部服务已停止"
}

function Do-Status {
  foreach ($role in $Roles) {
    $procId = Read-Pid $role
    if ($procId) {
      "{0,-10} running  pid={1,-8} identity={2}" -f $role, $procId, (Identity-For $role)
    } else {
      "{0,-10} stopped" -f $role
    }
  }
  Write-Host ""
  Write-Host "日志目录：$LogDir"
  $state = Get-InstallState
  if ($state) { Write-Host "安装态：$($state.data.status)" }
}

function Do-Install {
  Import-DotEnv $EnvFile
  if (-not (Wait-Http "http://127.0.0.1:$($Ports['config-api'])/readyz" 5)) {
    Die "config-api 未就绪；请先执行 start"
  }
  Invoke-Install
}

switch ($Action) {
  "start" { Do-Start }
  "stop" { Do-Stop }
  "restart" { Do-Stop; Do-Start }
  "status" { Do-Status }
  "install" { Do-Install }
}
