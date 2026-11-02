# 数据库运维镜像（ENT-08 G1）：备份、binlog 归档与 PITR 专用。
#
# 为什么不复用应用镜像：应用运行时基于 alpine，且**不应**携带数据库客户端与
# 对象存储凭据工具（供应链面与攻击面都会扩大）。运维镜像只在 CronJob 与
# 故障处置时拉起，可以带完整客户端。
#
# 为什么需要单独镜像：官方精简镜像 `mysql:8.0` 只含 mysqldump / mysql / mysqlpump，
# **不含 mysqlbinlog**（`rpm -ql mysql-community-server-minimal` 核对），
# 导致 runbook 里的 PITR 与 binlog 归档在容器内无法执行。
#
# 构建（上下文必须是仓库根，脚本随镜像分发）：
#   docker build -f deploy/backup/Dockerfile -t admin-frame-db-ops:1.0.0 .
#
# 校验镜像自带工具：
#   docker run --rm admin-frame-db-ops:1.0.0 sh -c 'mysqlbinlog --version; aws --version'

# 客户端来源：官方 MySQL debian 变体，含完整 mysqlbinlog / mysql / mysqldump。
FROM mysql:8.0-debian AS mysqlclient

FROM debian:bookworm-slim

# mysqlbinlog 的运行时依赖（ldd 实测）：libssl3、zlib1g；其余为标准库。
# 不引入数据库服务端，镜像保持纯客户端。
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        coreutils \
        curl \
        gzip \
        gnupg \
        libssl3 \
        tzdata \
        unzip \
        zlib1g \
    && rm -rf /var/lib/apt/lists/*

# 只拷客户端二进制，不拷服务端与库：体积从 ~800MB 降到 ~150MB。
COPY --from=mysqlclient /usr/bin/mysql /usr/local/bin/mysql
COPY --from=mysqlclient /usr/bin/mysqlbinlog /usr/local/bin/mysqlbinlog
COPY --from=mysqlclient /usr/bin/mysqldump /usr/local/bin/mysqldump

# AWS CLI v2：binlog 与全量备份上传对象存储（S3 兼容）。
# 版本固定，保证同一 commit 可重现构建（供应链要求）；升级时改这里并重跑演练。
ARG AWSCLI_VERSION=2.39.3
ARG TARGETARCH=amd64
RUN set -eux; \
    case "${TARGETARCH}" in \
        amd64) awsarch=x86_64 ;; \
        arm64) awsarch=aarch64 ;; \
        *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${awsarch}-${AWSCLI_VERSION}.zip" -o /tmp/awscli.zip; \
    unzip -q /tmp/awscli.zip -d /tmp; \
    /tmp/aws/install; \
    rm -rf /tmp/aws /tmp/awscli.zip

# 脚本随镜像分发，避免 CronJob 依赖 ConfigMap 与镜像版本不一致。
COPY scripts/backup/ /opt/backup/
RUN chmod 0755 /opt/backup/*.sh \
    && mkdir -p /var/backups/admin-frame /var/backups/binlog \
    && chown -R 10001:10001 /opt/backup /var/backups

# 与 CronJob 的 securityContext 对齐（runAsNonRoot / runAsUser 10001）。
USER 10001:10001
WORKDIR /opt/backup

ENV PATH="/usr/local/bin:${PATH}" \
    TZ=Etc/UTC

# 默认不执行任何操作：由 CronJob 指定 command/args，避免误跑导致意外备份或恢复。
CMD ["/bin/bash", "-c", "echo 'admin-frame db-ops image: specify a command (see deploy/backup/)'; exit 64"]
