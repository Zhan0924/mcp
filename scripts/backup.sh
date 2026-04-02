#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────────────
#  RAG MCP Server — 自动化备份脚本
#
#  备份内容：Redis RDB + Neo4j dump + 配置文件
#  使用方式：
#    手动执行: bash scripts/backup.sh
#    定时任务: 0 2 * * * /path/to/scripts/backup.sh >> /var/log/rag-backup.log 2>&1
#
#  恢复方式: bash scripts/backup.sh --restore <备份目录>
# ──────────────────────────────────────────────────────────────────────────────
set -euo pipefail

# ── 配置 ──────────────────────────────────────────────────────────────────────
BACKUP_ROOT="${BACKUP_ROOT:-/tmp/rag-backups}"
RETENTION_DAYS="${RETENTION_DAYS:-30}"
REDIS_HOST="${REDIS_HOST:-localhost}"
REDIS_PORT="${REDIS_PORT:-6379}"
REDIS_PASSWORD="${REDIS_PASSWORD:-}"
NEO4J_CONTAINER="${NEO4J_CONTAINER:-neo4j}"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_DIR="${BACKUP_ROOT}/${TIMESTAMP}"
LOG_PREFIX="[RAG-Backup]"

# ── 颜色输出 ──────────────────────────────────────────────────────────────────
info()  { echo "$(date '+%Y-%m-%d %H:%M:%S') ${LOG_PREFIX} INFO  $*"; }
warn()  { echo "$(date '+%Y-%m-%d %H:%M:%S') ${LOG_PREFIX} WARN  $*" >&2; }
error() { echo "$(date '+%Y-%m-%d %H:%M:%S') ${LOG_PREFIX} ERROR $*" >&2; }

# ── 恢复模式 ──────────────────────────────────────────────────────────────────
if [[ "${1:-}" == "--restore" ]]; then
    RESTORE_DIR="${2:-}"
    if [[ -z "$RESTORE_DIR" || ! -d "$RESTORE_DIR" ]]; then
        error "Usage: $0 --restore <backup_directory>"
        exit 1
    fi
    info "Starting restore from: $RESTORE_DIR"

    if [[ -f "$RESTORE_DIR/redis/dump.rdb" ]]; then
        info "Restoring Redis RDB..."
        docker cp "$RESTORE_DIR/redis/dump.rdb" redis-stack:/data/dump.rdb
        docker restart redis-stack
        info "Redis restored successfully"
    fi

    if [[ -f "$RESTORE_DIR/neo4j/neo4j.dump" ]]; then
        info "Restoring Neo4j dump..."
        docker cp "$RESTORE_DIR/neo4j/neo4j.dump" "${NEO4J_CONTAINER}:/tmp/neo4j.dump"
        docker exec "${NEO4J_CONTAINER}" neo4j-admin database load --from-path=/tmp neo4j --overwrite-destination 2>/dev/null || \
            warn "Neo4j restore requires manual intervention (stop DB first)"
        info "Neo4j restore attempted"
    fi

    info "Restore complete. Please verify service health."
    exit 0
fi

# ── 备份模式 ──────────────────────────────────────────────────────────────────
info "Starting backup to: $BACKUP_DIR"
mkdir -p "$BACKUP_DIR"/{redis,neo4j,config}

# 1. Redis 备份
info "Backing up Redis..."
REDIS_CLI_ARGS="-h $REDIS_HOST -p $REDIS_PORT"
if [[ -n "$REDIS_PASSWORD" ]]; then
    REDIS_CLI_ARGS="$REDIS_CLI_ARGS -a $REDIS_PASSWORD"
fi

# 触发 BGSAVE 并等待完成
redis-cli $REDIS_CLI_ARGS BGSAVE 2>/dev/null || docker exec redis-stack redis-cli BGSAVE 2>/dev/null || warn "BGSAVE trigger failed"
sleep 2

# 复制 RDB 文件
if docker cp redis-stack:/data/dump.rdb "$BACKUP_DIR/redis/dump.rdb" 2>/dev/null; then
    REDIS_SIZE=$(du -sh "$BACKUP_DIR/redis/dump.rdb" 2>/dev/null | cut -f1)
    info "Redis backup complete: $REDIS_SIZE"
else
    warn "Redis RDB copy failed (container may not exist)"
fi

# 导出 Redis key 统计
redis-cli $REDIS_CLI_ARGS INFO keyspace 2>/dev/null > "$BACKUP_DIR/redis/keyspace.txt" || true

# 2. Neo4j 备份
info "Backing up Neo4j..."
if docker exec "${NEO4J_CONTAINER}" neo4j-admin database dump --to-path=/tmp neo4j 2>/dev/null; then
    docker cp "${NEO4J_CONTAINER}:/tmp/neo4j.dump" "$BACKUP_DIR/neo4j/neo4j.dump"
    NEO4J_SIZE=$(du -sh "$BACKUP_DIR/neo4j/neo4j.dump" 2>/dev/null | cut -f1)
    info "Neo4j backup complete: $NEO4J_SIZE"
else
    # 降级：导出 Cypher 查询结果
    warn "Neo4j dump failed, exporting entity count..."
    docker exec "${NEO4J_CONTAINER}" cypher-shell -u neo4j -p password \
        "MATCH (n) RETURN labels(n) AS type, count(n) AS count" \
        2>/dev/null > "$BACKUP_DIR/neo4j/entity_stats.txt" || true
fi

# 3. 配置文件备份
info "Backing up config files..."
cp -f config.toml "$BACKUP_DIR/config/" 2>/dev/null || true
cp -f docker-compose.yml "$BACKUP_DIR/config/" 2>/dev/null || true
cp -f .env "$BACKUP_DIR/config/" 2>/dev/null || true

# 4. 生成备份清单
cat > "$BACKUP_DIR/manifest.json" <<EOF
{
    "timestamp": "$TIMESTAMP",
    "backup_dir": "$BACKUP_DIR",
    "components": {
        "redis": $(test -f "$BACKUP_DIR/redis/dump.rdb" && echo "true" || echo "false"),
        "neo4j": $(test -f "$BACKUP_DIR/neo4j/neo4j.dump" && echo "true" || echo "false"),
        "config": true
    },
    "server_version": "2.0.0",
    "created_by": "$(whoami)@$(hostname)"
}
EOF

# 5. 压缩备份
info "Compressing backup..."
cd "$BACKUP_ROOT"
tar -czf "${TIMESTAMP}.tar.gz" "$TIMESTAMP"
ARCHIVE_SIZE=$(du -sh "${TIMESTAMP}.tar.gz" | cut -f1)
rm -rf "$BACKUP_DIR"
info "Backup archived: ${BACKUP_ROOT}/${TIMESTAMP}.tar.gz ($ARCHIVE_SIZE)"

# 6. 清理旧备份
DELETED=$(find "$BACKUP_ROOT" -name "*.tar.gz" -mtime +"$RETENTION_DAYS" -delete -print | wc -l)
if [[ "$DELETED" -gt 0 ]]; then
    info "Cleaned up $DELETED old backups (retention: ${RETENTION_DAYS} days)"
fi

info "Backup complete!"
