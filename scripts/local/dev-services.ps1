# 本地开发：Windows 原生 PowerShell 编排（start|stop|restart|status）。
# 日常联调用本脚本；破坏性冒烟（TRUNCATE 密钥表）见 smoke-start-all.ps1。
#
# 用法：
#   pwsh -File scripts/local/dev-services.ps1 start
#   pwsh -File scripts/local/dev-services.ps1 stop
#   pwsh -File scripts/local/dev-services.ps1 restart
#   pwsh -File scripts/local/dev-services.ps1 status
#   pwsh -File scripts/local/dev-services.ps1 start -NoBuild -Migrate
#
# 环境变量文件默认 scripts/local/.env（从 env.example 复制）。

[CmdletBinding()]
param(
  [Parameter(Position = 0, Mandatory = $true)]
  [ValidateSet("start", "stop", "restart", "status")]
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
}

switch ($Action) {
  "start" { Do-Start }
  "stop" { Do-Stop }
  "restart" { Do-Stop; Do-Start }
  "status" { Do-Status }
}
