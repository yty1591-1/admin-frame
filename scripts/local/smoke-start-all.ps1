# 本机冒烟（破坏性）：固定 LocalKEK → TRUNCATE 密钥表 → 起五进程 → healthz + 登录。
# 日常联调请用：
#   pwsh -File scripts/local/dev-services.ps1 start
#   或 make local-start（Git Bash）
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
if (-not (Test-Path (Join-Path $Root "go.mod"))) {
  $Root = (Get-Location).Path
}
Set-Location $Root

$env:ADMIN_FRAME_DB_DRIVER = "mysql"
$env:ADMIN_FRAME_DB_DSN = 'root:root@tcp(127.0.0.1:3306)/admin_frame_test?parseTime=true&loc=UTC&charset=utf8mb4'
$env:ADMIN_FRAME_REDIS_ADDR = "127.0.0.1:6379"
$env:CONFIGCENTER_ENV = "dev"
$env:CONFIG_API_URL = "http://127.0.0.1:8085"
$env:CONFIGCENTER_LOCAL_KEK_HEX = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
$env:CONFIGCENTER_CACHE_DIR = Join-Path $Root "var\configcenter-cache-smoke"
# worker 通知黑名单 HMAC 盐（装配期必填；本地冒烟固定值，非生产密钥）
$env:ADMIN_FRAME_NOTIFICATION_BLACKLIST_SALT = "smoke-local-blacklist-salt-32b!!"


New-Item -ItemType Directory -Force -Path (Join-Path $Root "bin\run\logs") | Out-Null
New-Item -ItemType Directory -Force -Path $env:CONFIGCENTER_CACHE_DIR | Out-Null

Write-Host "==> kill leftover listeners (Windows + WSL)"
foreach ($p in 8085,8081,8082,8083,8090) {
  Get-NetTCPConnection -State Listen -LocalPort $p -ErrorAction SilentlyContinue |
    ForEach-Object { Stop-Process -Id $_.OwningProcess -Force -ErrorAction SilentlyContinue }
}
Get-Process -Name "config-api","admin-api","biz-api","c-api","worker" -ErrorAction SilentlyContinue |
  Stop-Process -Force -ErrorAction SilentlyContinue
wsl -e bash -lc "pkill -f './bin/config-api|./bin/admin-api|./bin/biz-api|./bin/c-api|./bin/worker|bin/config-api|bin/admin-api' || true" | Out-Null
Start-Sleep -Seconds 2

Write-Host "==> reset encrypted secrets"
docker exec admin-frame-integration-mysql-1 mysql -uroot -proot admin_frame_test -e "SET FOREIGN_KEY_CHECKS=0; TRUNCATE TABLE config_secret_grant; TRUNCATE TABLE config_secret_review; TRUNCATE TABLE config_secret_version; TRUNCATE TABLE config_secret; SET FOREIGN_KEY_CHECKS=1;"

Write-Host "==> build"
go build -ldflags "-s -w" -o bin\config-api.exe ./cmd/config-api
go build -ldflags "-s -w" -o bin\admin-api.exe ./cmd/admin-api
go build -ldflags "-s -w" -o bin\biz-api.exe ./cmd/biz-api
go build -ldflags "-s -w" -o bin\c-api.exe ./cmd/c-api
go build -ldflags "-s -w" -o bin\worker.exe ./cmd/worker

function Start-Svc([string]$Name, [string]$Exe, [string]$Identity) {
  $log = Join-Path $Root "bin\run\logs\$Name.log"
  $err = Join-Path $Root "bin\run\logs\$Name.err.log"
  Remove-Item $log,$err -ErrorAction SilentlyContinue
  $env:CONFIG_SERVICE_IDENTITY = $Identity
  $proc = Start-Process -FilePath (Join-Path $Root "bin\$Exe") -WorkingDirectory $Root `
    -RedirectStandardOutput $log -RedirectStandardError $err -PassThru -WindowStyle Hidden
  Write-Host "started $Name pid=$($proc.Id) identity=$Identity"
  return $proc
}

function Wait-Http([string]$Url, [int]$Seconds = 90) {
  $deadline = (Get-Date).AddSeconds($Seconds)
  while ((Get-Date) -lt $deadline) {
    try {
      $r = Invoke-WebRequest -Uri $Url -UseBasicParsing -TimeoutSec 2
      if ($r.StatusCode -ge 200 -and $r.StatusCode -lt 300) { return }
    } catch {}
    Start-Sleep -Milliseconds 400
  }
  throw "timeout waiting for $Url"
}

function Assert-Alive([System.Diagnostics.Process]$Proc, [string]$Name) {
  Start-Sleep -Milliseconds 800
  if ($Proc.HasExited) {
    $err = Join-Path $Root "bin\run\logs\$Name.err.log"
    if (Test-Path $err) { Get-Content $err -Tail 30 | Write-Host }
    throw "$Name exited early code=$($Proc.ExitCode)"
  }
}

Write-Host "==> start config-api (fixed KEK)"
$configProc = Start-Svc "config-api" "config-api.exe" "config-api"
Assert-Alive $configProc "config-api"
Wait-Http "http://127.0.0.1:8085/readyz" 90
Assert-Alive $configProc "config-api"
Write-Host "config-api ready"

# 确认 resolve 可用
$resolveBody = '{"env":"dev","namespace":"platform","names":["auth.jwt.private_key"]}'
$resolve = Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:8085/internal/v1/configs/secrets/resolve" `
  -Headers @{ "X-Service-Identity" = "admin-api" } -ContentType "application/json" -Body $resolveBody
if (-not $resolve.data.items) { throw "jwt resolve failed: $($resolve | ConvertTo-Json -Depth 5)" }
Write-Host "OK jwt resolve"

Write-Host "==> start business processes"
$admin = Start-Svc "admin-api" "admin-api.exe" "admin-api"
$biz = Start-Svc "biz-api" "biz-api.exe" "biz-api"
$capi = Start-Svc "c-api" "c-api.exe" "c-api"
$worker = Start-Svc "worker" "worker.exe" "worker"
Assert-Alive $admin "admin-api"
Assert-Alive $biz "biz-api"
Assert-Alive $capi "c-api"
# worker 装配较慢（DB/Outbox），多给一点窗口
Start-Sleep -Seconds 2
Assert-Alive $worker "worker"

Wait-Http "http://127.0.0.1:8081/readyz" 90
Wait-Http "http://127.0.0.1:8082/readyz" 90
Wait-Http "http://127.0.0.1:8083/readyz" 90
Wait-Http "http://127.0.0.1:8090/readyz" 90
Assert-Alive $worker "worker"
Write-Host "business APIs + worker ready"

Write-Host "==> smoke healthz"
foreach ($port in 8085,8081,8082,8083,8090) {
  $hz = Invoke-WebRequest -Uri "http://127.0.0.1:$port/healthz" -UseBasicParsing
  if ($hz.StatusCode -lt 200 -or $hz.StatusCode -ge 300) {
    throw "healthz :$port => $($hz.StatusCode)"
  }
  Write-Host "OK :$port/healthz ($($hz.StatusCode))"
}

Write-Host "==> smoke admin login"
$body = '{"subject":"admin@example.com","password":"Admin#Bootstrap2026"}'
try {
  $login = Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:8081/admin/v1/auth/login" `
    -ContentType "application/json" -Body $body
} catch {
  Get-Content (Join-Path $Root "bin\run\logs\admin-api.err.log") -Tail 40 -ErrorAction SilentlyContinue
  Get-Content (Join-Path $Root "bin\run\logs\admin-api.log") -Tail 20 -ErrorAction SilentlyContinue
  throw
}
$data = $login.data
if (-not $data) { $data = $login }
$token = $null
if ($data.access_token) { $token = $data.access_token }
elseif ($data.tokens -and $data.tokens.access_token) { $token = $data.tokens.access_token }
$pwdChange = $false
if ($data.password_change_required -eq $true) { $pwdChange = $true }
elseif ($data.password_change_ticket) { $pwdChange = $true }
if ($token) {
  Write-Host "OK admin login (token len=$($token.Length))"
} elseif ($pwdChange) {
  Write-Host "OK admin login (password_change_required)"
} else {
  Write-Host ($login | ConvertTo-Json -Depth 8)
  Get-Content (Join-Path $Root "bin\run\logs\admin-api.err.log") -Tail 40 -ErrorAction SilentlyContinue
  throw "login response missing access_token / password_change_ticket"
}

Write-Host "SMOKE PASS"
Write-Host "pids: config=$($configProc.Id) admin=$($admin.Id) biz=$($biz.Id) c=$($capi.Id) worker=$($worker.Id)"
