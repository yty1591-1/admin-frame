# 本机 prod 演练：Windows 原生 PowerShell 编排（start|stop|restart|status|migrate）。
#
# 用法：
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prod/prod-services.ps1 start
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prod/prod-services.ps1 stop
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prod/prod-services.ps1 restart
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prod/prod-services.ps1 status
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prod/prod-services.ps1 migrate
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prod/prod-services.ps1 start -NoBuild -Migrate
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/prod/prod-services.ps1 start -Strict
#
# 启动策略（降级启动，默认）：
#   config-api 是硬前置，未就绪则整体失败并回滚；
#   worker / admin-api / biz-api / c-api 任一启动失败只记录，不停止已起进程，
#   便于打开 Admin /setup/install 修复配置。-Strict 恢复「全有或全无」。
#
# 环境变量文件默认 scripts/prod/.env（从 env.example 复制）。
# 最小启动：不自动 begin/apply/complete；业务配置用 Admin /setup/install。

[CmdletBinding()]
param(
  [Parameter(Position = 0, Mandatory = $true)]
  [ValidateSet("start", "stop", "restart", "status", "migrate")]
  [string]$Action,

  [switch]$NoBuild,
  [switch]$SkipHealth,
  [switch]$Migrate,
  # 默认「降级启动」：单个可降级进程失败不回滚已起进程，便于进 Admin 修配置。
  # -Strict 恢复全有或全无（任一进程失败即停掉全部）。
  [switch]$Strict,
  [string]$EnvFile = ""
)

$ErrorActionPreference = "Stop"
$Root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
Set-Location $Root

$BinDir = Join-Path $Root "bin"
$RuntimeDir = Join-Path $BinDir "run-prod"
$PidDir = Join-Path $RuntimeDir "pids"
$LogDir = Join-Path $RuntimeDir "logs"
if (-not $EnvFile) { $EnvFile = Join-Path $Root "scripts\prod\.env" }

$Roles = @("config-api", "worker", "admin-api", "biz-api", "c-api")
$Ports = @{
  "config-api" = 8085
  "admin-api"  = 8081
  "biz-api"    = 8082
  "c-api"      = 8083
  "worker"     = 8090
}
$NodeIds = @{
  "config-api" = "1"
  "admin-api"  = "2"
  "biz-api"    = "3"
  "c-api"      = "4"
  "worker"     = "5"
}

$script:RollbackOnDie = $false
$script:SavedVaultToken = $null

function Identity-For([string]$Role) { return $Role }
function Write-Log([string]$Message) { Write-Host $Message }
function Write-Warn([string]$Message) { Write-Host "WARN: $Message" -ForegroundColor Yellow }

function Die([string]$Message) {
  Write-Host "ERROR: $Message" -ForegroundColor Red
  if ($script:RollbackOnDie) {
    $script:RollbackOnDie = $false
    Write-Warn "启动失败，回滚已启动进程"
    Do-Stop
  }
  exit 1
}

function Show-RoleLogs([string]$Role, [int]$Lines = 50) {
  $log = Join-Path $LogDir "$Role.log"
  $err = Join-Path $LogDir "$Role.err.log"
  if (Test-Path $log) {
    Write-Warn "$Role stdout（最近 $Lines 行）："
    Get-Content $log -Tail $Lines -ErrorAction SilentlyContinue | Write-Host
  }
  if (Test-Path $err) {
    Write-Warn "$Role stderr（最近 $Lines 行）："
    Get-Content $err -Tail $Lines -ErrorAction SilentlyContinue | Write-Host
  }
}

function Import-DotEnv([string]$Path) {
  if (-not (Test-Path $Path)) {
    Die "缺少 $Path；请先：Copy-Item scripts\prod\env.example scripts\prod\.env"
  }
  # 必须 UTF-8：系统默认编码（如 CP936）会误读中文注释并吞掉后续 KEY= 行。
  Get-Content -LiteralPath $Path -Encoding UTF8 | ForEach-Object {
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
  $vaultTokenFile = Join-Path $Root "var\prod-vault\root.token"
  if (Test-Path $vaultTokenFile) {
    $env:VAULT_TOKEN = (Get-Content -LiteralPath $vaultTokenFile -Encoding UTF8 -Raw).Trim()
  }
  $script:SavedVaultToken = $env:VAULT_TOKEN
  if (-not $env:CONFIGCENTER_ENV) { $env:CONFIGCENTER_ENV = "prod" }
  if (-not $env:CONFIGCENTER_CACHE_DIR) { $env:CONFIGCENTER_CACHE_DIR = "var\configcenter-cache-prod" }
  if (-not $env:CONFIGCENTER_KEK_DRIVER) { $env:CONFIGCENTER_KEK_DRIVER = "vault" }
  if (-not $env:CONFIG_API_URL) { $env:CONFIG_API_URL = "https://127.0.0.1:8085" }
  if (-not $env:CONFIG_API_MTLS) { $env:CONFIG_API_MTLS = "require" }
  New-Item -ItemType Directory -Force -Path $env:CONFIGCENTER_CACHE_DIR, (Join-Path $Root "var\prod-storage") | Out-Null

  if ($env:CONFIGCENTER_ENV -ne "prod") { Die "CONFIGCENTER_ENV 必须为 prod" }
  if ($env:CONFIGCENTER_KEK_DRIVER -ne "vault") { Die "prod 演练要求 CONFIGCENTER_KEK_DRIVER=vault" }
  if ($env:CONFIG_API_MTLS -ne "require") { Die "prod 演练要求 CONFIG_API_MTLS=require" }
  if (-not $env:VAULT_ADDR -or -not $env:VAULT_TOKEN) { Die "缺少 VAULT_ADDR / VAULT_TOKEN（先 make prod-vault-init）" }
  if (-not $env:CONFIG_API_TLS_CERT -or -not (Test-Path $env:CONFIG_API_TLS_CERT)) {
    Die "缺少服务端证书：$($env:CONFIG_API_TLS_CERT)（先 make prod-certs）"
  }
  if (-not $env:CONFIG_API_CLIENT_CERT -or -not (Test-Path $env:CONFIG_API_CLIENT_CERT)) {
    Die "缺少客户端证书：$($env:CONFIG_API_CLIENT_CERT)"
  }
  if (-not $env:CONFIG_API_NAMESPACE_ALLOWLIST) { Die "缺少 CONFIG_API_NAMESPACE_ALLOWLIST" }
  if (-not $env:CONFIG_BOOTSTRAP_GRANT_IDENTITY) { Die "缺少 CONFIG_BOOTSTRAP_GRANT_IDENTITY（prod CC-R-05）" }
  # prod-like 播种固定 storage.driver=s3，而连接参数属部署侧注入（CONFIG_STANDARDS §3.1.1）。
  # 缺失时 admin-api / worker / biz-api 都会在 Validate 阶段退出，向导都打不开。
  if (-not $env:ADMIN_FRAME_STORAGE_S3_BUCKET -or -not $env:ADMIN_FRAME_STORAGE_S3_REGION) {
    Write-Warn "缺少 ADMIN_FRAME_STORAGE_S3_BUCKET / REGION：prod-like 播种为 storage.driver=s3，业务进程将因 's3 requires bucket and region' 退出"
    Write-Warn "请在 $Path 补齐（见 scripts/prod/env.example 的部署侧 overlay 段）"
  }
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

function Wait-Readyz([string]$Role, [int]$Port, [int]$Seconds = 120) {
  $scheme = if ($Role -eq "config-api") { "https" } else { "http" }
  $url = "${scheme}://127.0.0.1:${Port}/readyz"
  $deadline = (Get-Date).AddSeconds($Seconds)
  $py = Get-Command python -ErrorAction SilentlyContinue
  if (-not $py) { $py = Get-Command python3 -ErrorAction SilentlyContinue }
  while ((Get-Date) -lt $deadline) {
    if (-not (Read-Pid $Role)) {
      Write-Warn "$Role 进程已退出"
      return $false
    }
    try {
      if ($scheme -eq "https") {
        if (-not $py) { Start-Sleep -Milliseconds 500; continue }
        $probeCert = Join-Path $Root "var\prod-mtls\client-config-api.crt"
        $probeKey = Join-Path $Root "var\prod-mtls\client-config-api.key"
        if (-not (Test-Path $probeCert)) { $probeCert = $env:CONFIG_API_CLIENT_CERT }
        if (-not (Test-Path $probeKey)) { $probeKey = $env:CONFIG_API_CLIENT_KEY }
        $ca = $env:CONFIG_API_TLS_CA
        if (-not $ca -or -not $probeCert -or -not $probeKey) {
          Start-Sleep -Milliseconds 500
          continue
        }
        if (-not [System.IO.Path]::IsPathRooted($ca)) { $ca = Join-Path $Root $ca }
        if (-not [System.IO.Path]::IsPathRooted($probeCert)) { $probeCert = Join-Path $Root $probeCert }
        if (-not [System.IO.Path]::IsPathRooted($probeKey)) { $probeKey = Join-Path $Root $probeKey }
        if (-not ((Test-Path $ca) -and (Test-Path $probeCert) -and (Test-Path $probeKey))) {
          Start-Sleep -Milliseconds 500
          continue
        }
        $prevEap = $ErrorActionPreference
        $ErrorActionPreference = "Continue"
        & $py.Source -c @"
import ssl, urllib.request, sys
ctx = ssl.create_default_context(cafile=r'$ca')
ctx.load_cert_chain(certfile=r'$probeCert', keyfile=r'$probeKey')
urllib.request.urlopen(r'$url', context=ctx, timeout=2).read()
"@ 2>$null
        $pyCode = $LASTEXITCODE
        $ErrorActionPreference = $prevEap
        if ($pyCode -eq 0) { return $true }
      } else {
        $r = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
        if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 300) { return $true }
      }
    } catch {}
    Start-Sleep -Milliseconds 500
  }
  return $false
}

function Start-Role([string]$Role) {
  $exe = Exe-Path $Role
  if (-not (Test-Path $exe)) {
    Write-Warn "二进制不存在：$exe"
    return $false
  }
  $existing = Read-Pid $Role
  if ($existing) {
    Write-Warn "$Role 已在运行（pid=$existing），跳过"
    return $true
  }
  $identity = Identity-For $Role
  $nodeId = $NodeIds[$Role]
  $env:CONFIG_SERVICE_IDENTITY = $identity
  $env:ADMIN_FRAME_ID_NODE_ID = $nodeId
  # 最小权限：仅 config-api 注入 Vault token。
  if ($Role -eq "config-api") {
    if ($script:SavedVaultToken) { $env:VAULT_TOKEN = $script:SavedVaultToken }
  } else {
    Remove-Item Env:VAULT_TOKEN -ErrorAction SilentlyContinue
  }
  $clientCert = Join-Path $Root "var\prod-mtls\client-$Role.crt"
  $clientKey = Join-Path $Root "var\prod-mtls\client-$Role.key"
  if ((Test-Path $clientCert) -and (Test-Path $clientKey)) {
    $env:CONFIG_API_CLIENT_CERT = $clientCert
    $env:CONFIG_API_CLIENT_KEY = $clientKey
  }
  New-Item -ItemType Directory -Force -Path $PidDir, $LogDir | Out-Null
  $log = Join-Path $LogDir "$Role.log"
  $err = Join-Path $LogDir "$Role.err.log"
  $meta = Join-Path $LogDir "$Role.meta.log"
  # Start-Process 重定向会截断 log/err；横幅写入 meta，避免被冲掉。
  "=== $Role start $(Get-Date -Format o) identity=$identity node_id=$nodeId ===" | Set-Content -Path $meta
  $proc = Start-Process -FilePath $exe -WorkingDirectory $Root `
    -RedirectStandardOutput $log -RedirectStandardError $err -PassThru -WindowStyle Hidden
  Set-Content -Path (Join-Path $PidDir "$Role.pid") -Value $proc.Id -NoNewline
  # 恢复 token，供后续启动逻辑读取。
  if ($script:SavedVaultToken) { $env:VAULT_TOKEN = $script:SavedVaultToken }
  Start-Sleep -Milliseconds 1000
  if ($proc.HasExited) {
    Show-RoleLogs $Role 50
    Write-Warn "$Role 启动即退出 code=$($proc.ExitCode)"
    Remove-Item (Join-Path $PidDir "$Role.pid") -ErrorAction SilentlyContinue
    return $false
  }
  Write-Log "$Role 已启动（pid=$($proc.Id), identity=$identity, node_id=$nodeId）"
  return $true
}

function Stop-Role([string]$Role) {
  $procId = Read-Pid $Role
  $pidFile = Join-Path $PidDir "$Role.pid"
  if (-not $procId) {
    # pidfile 丢失时，按进程名兜底清理孤儿（仅限本仓库 bin 下同名 exe）。
    $name = $Role
    Get-Process -Name $name -ErrorAction SilentlyContinue | ForEach-Object {
      try {
        if ($_.Path -and ($_.Path -like "$BinDir*")) {
          Write-Log "停止孤儿 $Role（pid=$($_.Id)）..."
          Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue
        }
      } catch {}
    }
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

function Do-MigrateOnly {
  Import-DotEnv $EnvFile
  Invoke-Migrate
  Write-Log "migrate up 完成"
}

function Do-Start {
  New-Item -ItemType Directory -Force -Path $PidDir, $LogDir | Out-Null
  Import-DotEnv $EnvFile
  $script:RollbackOnDie = $true
  if (-not $NoBuild) { Build-All }
  if ($Migrate) { Invoke-Migrate }

  # config-api 是硬前置：它起不来，其余进程与安装向导都不可用，直接回滚。
  Start-Role "config-api" | Out-Null
  if (-not $SkipHealth) {
    if (-not (Wait-Readyz "config-api" $Ports["config-api"] 120)) {
      Show-RoleLogs "config-api" 50
      Die "config-api 未就绪"
    }
    Write-Log "config-api ready（最小启动；业务播种请打开 Admin /setup/install）"
  }

  foreach ($role in @("worker", "admin-api", "biz-api", "c-api")) {
    Start-Role $role | Out-Null
    if ($role -eq "worker") { Start-Sleep -Seconds 1 }
  }

  # 未完成安装时业务 /readyz 为 503（ADR-0017），不因此失败启动；仅确认进程仍存活。
  # 默认降级启动：失败进程只汇总，不停已起进程，避免「一处配置错 → Admin 也进不去」。
  $failed = @()
  foreach ($role in @("worker", "admin-api", "biz-api", "c-api")) {
    if (-not (Read-Pid $role)) {
      Show-RoleLogs $role 50
      $failed += $role
    }
  }

  if ($failed.Count -gt 0) {
    if ($Strict) {
      Write-Warn "以下进程未启动：$($failed -join ', ')（-Strict：回滚全部进程）"
      Die "启动失败"
    }
    Write-Warn "以下进程未启动：$($failed -join ', ')；其余进程保持运行"
    if (-not (Read-Pid "admin-api")) {
      Write-Warn "admin-api 未运行，安装向导不可用；请按日志修正配置后重试：make prod-restart"
    } else {
      Write-Warn "admin-api 可用，可打开 Admin /setup/install 补齐配置后重启失败进程"
    }
    $script:RollbackOnDie = $false
    Write-Log "降级启动完成。日志：$LogDir"
    exit 1
  }

  Write-Warn "业务 /readyz 在 install 未 ready 前可为 503；请打开 Admin /setup/install 完成播种"
  $script:RollbackOnDie = $false
  Write-Log "启动完成。日志：$LogDir"
}

function Do-Stop {
  $prev = $script:RollbackOnDie
  $script:RollbackOnDie = $false
  foreach ($role in @("c-api", "biz-api", "admin-api", "worker", "config-api")) {
    Stop-Role $role
  }
  $script:RollbackOnDie = $prev
  Write-Log "全部服务已停止"
}

function Do-Status {
  foreach ($role in $Roles) {
    $procId = Read-Pid $role
    if ($procId) {
      "{0,-10} running  pid={1,-8} identity={2} node_id={3}" -f $role, $procId, (Identity-For $role), $NodeIds[$role]
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
  "migrate" { Do-MigrateOnly }
}
