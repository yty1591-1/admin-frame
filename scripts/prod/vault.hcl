# 本机 prod 演练 Vault 配置（file 存储，重启后 Transit 密钥材料可恢复）。
# 禁止用于任何真实环境。
ui = true
disable_mlock = true

storage "file" {
  path = "/vault/file"
}

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = "true"
}

api_addr     = "http://127.0.0.1:8200"
cluster_addr = "https://127.0.0.1:8201"
