#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND_DIR="$ROOT_DIR/backend"
FRONTEND_DIR="$ROOT_DIR/frontend"
RUNTIME_DIR="$ROOT_DIR/.local/grok-oauth-test"
LOG_DIR="$RUNTIME_DIR/logs"
PID_DIR="$RUNTIME_DIR/pids"

POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-sub2api-local-postgres}"
REDIS_CONTAINER="${REDIS_CONTAINER:-sub2api-local-redis}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-local_sub2api_password}"
POSTGRES_PORT="${POSTGRES_PORT:-5432}"
REDIS_PORT="${REDIS_PORT:-6379}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@local.test}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-12345678}"
JWT_SECRET="${JWT_SECRET:-local-dev-jwt-secret-change-me-32bytes}"
SERVER_PORT="${SERVER_PORT:-8080}"
FRONTEND_HOST="${FRONTEND_HOST:-127.0.0.1}"
FRONTEND_PORT="${FRONTEND_PORT:-3000}"

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少命令：$1" >&2
    return 1
  fi
}

ensure_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    cat >&2 <<'EOF'
缺少 Docker。
请先安装 Docker Desktop（Windows/macOS）或 Docker Engine（Linux），然后重新运行本脚本。
本脚本不会自动安装 Docker，因为安装 Docker 是系统级改动。
EOF
    exit 1
  fi
}

ensure_runtime_dirs() {
  mkdir -p "$LOG_DIR" "$PID_DIR" "$RUNTIME_DIR/data"
}

container_exists() {
  docker container inspect "$1" >/dev/null 2>&1
}

container_running() {
  [ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null || true)" = "true" ]
}

pid_running() {
  local pid_file="$1"
  [ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" >/dev/null 2>&1
}

wait_for_http() {
  local url="$1"
  local name="$2"
  local i
  for i in $(seq 1 60); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      echo "$name 已就绪：$url"
      return 0
    fi
    sleep 1
  done
  echo "$name 未在预期时间内就绪：$url" >&2
  return 1
}

start_postgres() {
  if container_running "$POSTGRES_CONTAINER"; then
    echo "Postgres 已运行：$POSTGRES_CONTAINER"
    return
  fi
  if container_exists "$POSTGRES_CONTAINER"; then
    echo "启动已有 Postgres 容器：$POSTGRES_CONTAINER"
    docker start "$POSTGRES_CONTAINER" >/dev/null
    return
  fi
  echo "创建并启动本地 Postgres：$POSTGRES_CONTAINER"
  docker run -d \
    --name "$POSTGRES_CONTAINER" \
    -e POSTGRES_USER=sub2api \
    -e POSTGRES_PASSWORD="$POSTGRES_PASSWORD" \
    -e POSTGRES_DB=sub2api \
    -p "127.0.0.1:$POSTGRES_PORT:5432" \
    postgres:18-alpine >/dev/null
}

start_redis() {
  if container_running "$REDIS_CONTAINER"; then
    echo "Redis 已运行：$REDIS_CONTAINER"
    return
  fi
  if container_exists "$REDIS_CONTAINER"; then
    echo "启动已有 Redis 容器：$REDIS_CONTAINER"
    docker start "$REDIS_CONTAINER" >/dev/null
    return
  fi
  echo "创建并启动本地 Redis：$REDIS_CONTAINER"
  docker run -d \
    --name "$REDIS_CONTAINER" \
    -p "127.0.0.1:$REDIS_PORT:6379" \
    redis:8-alpine >/dev/null
}

start_deps() {
  ensure_docker
  start_postgres
  start_redis
  echo "本地依赖已启动：Postgres 127.0.0.1:$POSTGRES_PORT，Redis 127.0.0.1:$REDIS_PORT"
}

start_backend_bg() {
  need_cmd go
  ensure_runtime_dirs
  if pid_running "$PID_DIR/backend.pid"; then
    echo "后端已运行，PID=$(cat "$PID_DIR/backend.pid")"
    return
  fi
  echo "后台启动后端：http://127.0.0.1:$SERVER_PORT"
  (
    cd "$BACKEND_DIR"
    AUTO_SETUP=true \
      DATA_DIR="$RUNTIME_DIR/data" \
      SERVER_HOST=127.0.0.1 \
      SERVER_PORT="$SERVER_PORT" \
      SERVER_MODE=debug \
      DATABASE_HOST=127.0.0.1 \
      DATABASE_PORT="$POSTGRES_PORT" \
      DATABASE_USER=sub2api \
      DATABASE_PASSWORD="$POSTGRES_PASSWORD" \
      DATABASE_DBNAME=sub2api \
      DATABASE_SSLMODE=disable \
      REDIS_HOST=127.0.0.1 \
      REDIS_PORT="$REDIS_PORT" \
      REDIS_PASSWORD= \
      ADMIN_EMAIL="$ADMIN_EMAIL" \
      ADMIN_PASSWORD="$ADMIN_PASSWORD" \
      JWT_SECRET="$JWT_SECRET" \
      go run ./cmd/server >"$LOG_DIR/backend.log" 2>&1
  ) &
  echo "$!" > "$PID_DIR/backend.pid"
}

start_frontend_bg() {
  need_cmd pnpm
  ensure_runtime_dirs
  if pid_running "$PID_DIR/frontend.pid"; then
    echo "前端已运行，PID=$(cat "$PID_DIR/frontend.pid")"
    return
  fi
  echo "后台启动前端：http://$FRONTEND_HOST:$FRONTEND_PORT"
  (
    cd "$FRONTEND_DIR"
    pnpm run dev --host "$FRONTEND_HOST" --port "$FRONTEND_PORT" >"$LOG_DIR/frontend.log" 2>&1
  ) &
  echo "$!" > "$PID_DIR/frontend.pid"
}

run_backend() {
  need_cmd go
  echo "启动后端：http://127.0.0.1:$SERVER_PORT"
  echo "管理员账号：$ADMIN_EMAIL / $ADMIN_PASSWORD"
  (cd "$BACKEND_DIR" && \
    AUTO_SETUP=true \
    SERVER_HOST=127.0.0.1 \
    SERVER_PORT="$SERVER_PORT" \
    SERVER_MODE=debug \
    DATABASE_HOST=127.0.0.1 \
    DATABASE_PORT="$POSTGRES_PORT" \
    DATABASE_USER=sub2api \
    DATABASE_PASSWORD="$POSTGRES_PASSWORD" \
    DATABASE_DBNAME=sub2api \
    DATABASE_SSLMODE=disable \
    REDIS_HOST=127.0.0.1 \
    REDIS_PORT="$REDIS_PORT" \
    REDIS_PASSWORD= \
    ADMIN_EMAIL="$ADMIN_EMAIL" \
    ADMIN_PASSWORD="$ADMIN_PASSWORD" \
    JWT_SECRET="$JWT_SECRET" \
    go run ./cmd/server)
}

run_frontend() {
  need_cmd pnpm
  echo "启动前端：http://$FRONTEND_HOST:$FRONTEND_PORT"
  (cd "$FRONTEND_DIR" && pnpm run dev --host "$FRONTEND_HOST" --port "$FRONTEND_PORT")
}

stop_pid() {
  local name="$1"
  local pid_file="$PID_DIR/$name.pid"
  if pid_running "$pid_file"; then
    kill "$(cat "$pid_file")" >/dev/null 2>&1 || true
    echo "已停止 $name，PID=$(cat "$pid_file")"
  fi
  rm -f "$pid_file"
}

stop_app() {
  stop_pid frontend
  stop_pid backend
}

stop_deps() {
  ensure_docker
  docker stop "$POSTGRES_CONTAINER" "$REDIS_CONTAINER" >/dev/null 2>&1 || true
  echo "本地依赖已停止。"
}

start_all() {
  need_cmd curl
  start_deps
  start_backend_bg
  wait_for_http "http://127.0.0.1:$SERVER_PORT/setup/status" "后端" || true
  start_frontend_bg
  wait_for_http "http://$FRONTEND_HOST:$FRONTEND_PORT" "前端" || true
  print_status
  print_steps
}

print_logs() {
  ensure_runtime_dirs
  echo "日志目录：$LOG_DIR"
  echo "--- backend.log ---"
  tail -n 80 "$LOG_DIR/backend.log" 2>/dev/null || true
  echo "--- frontend.log ---"
  tail -n 80 "$LOG_DIR/frontend.log" 2>/dev/null || true
}

print_status() {
  ensure_runtime_dirs
  echo "运行状态："
  if command -v docker >/dev/null 2>&1; then
    docker ps --filter "name=$POSTGRES_CONTAINER" --filter "name=$REDIS_CONTAINER" --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}' || true
  else
    echo "Docker 不可用"
  fi
  if pid_running "$PID_DIR/backend.pid"; then
    echo "backend: running PID=$(cat "$PID_DIR/backend.pid") http://127.0.0.1:$SERVER_PORT"
  elif curl -fsS "http://127.0.0.1:$SERVER_PORT/setup/status" >/dev/null 2>&1; then
    echo "backend: reachable http://127.0.0.1:$SERVER_PORT (PID 未由脚本记录)"
  else
    echo "backend: stopped"
  fi
  if pid_running "$PID_DIR/frontend.pid"; then
    echo "frontend: running PID=$(cat "$PID_DIR/frontend.pid") http://$FRONTEND_HOST:$FRONTEND_PORT"
  elif curl -fsS "http://$FRONTEND_HOST:$FRONTEND_PORT" >/dev/null 2>&1; then
    echo "frontend: reachable http://$FRONTEND_HOST:$FRONTEND_PORT (PID 未由脚本记录)"
  else
    echo "frontend: stopped"
  fi
  echo "logs: $LOG_DIR"
}

print_steps() {
  cat <<EOF

本地 Grok OAuth 测试步骤：
1. 打开 http://$FRONTEND_HOST:$FRONTEND_PORT
2. 使用管理员账号登录：$ADMIN_EMAIL / $ADMIN_PASSWORD
3. 进入：账号管理 -> 添加账号 -> Grok -> Grok OAuth 登录
4. 点击“开始 Grok 登录”
5. 在 xAI 页面完成授权
6. 授权后如果浏览器跳到 http://127.0.0.1:56121/callback?code=...&state=... 且页面打不开，复制地址栏完整 URL
7. 粘贴回 Grok OAuth 登录弹窗，点击“创建 Grok 账号”
8. 账号列表应出现 platform=grok、type=oauth 的账号

常用命令：
- 一键启动：tools/local-grok-oauth-test.sh up
- 查看状态：tools/local-grok-oauth-test.sh status
- 查看日志：tools/local-grok-oauth-test.sh logs
- 停止应用：tools/local-grok-oauth-test.sh down
- 停止依赖：tools/local-grok-oauth-test.sh stop

注意：不要把 callback URL、code、token 发到聊天里。
EOF
}

case "${1:-help}" in
  up)
    start_all
    ;;
  down)
    stop_app
    ;;
  deps)
    start_deps
    ;;
  stop)
    stop_app
    stop_deps
    ;;
  backend)
    run_backend
    ;;
  frontend)
    run_frontend
    ;;
  status)
    print_status
    ;;
  logs)
    print_logs
    ;;
  steps)
    print_steps
    ;;
  check)
    need_cmd go
    need_cmd pnpm
    need_cmd curl
    ensure_docker
    echo "本地依赖检查通过。"
    ;;
  help|*)
    cat <<'EOF'
用法：tools/local-grok-oauth-test.sh <command>

commands:
  check     检查 go/pnpm/curl/docker 是否存在
  up        一键启动依赖、后端、前端，并打印测试步骤
  down      停止后台后端/前端
  deps      只启动隔离的本地 Postgres/Redis 容器
  backend   前台启动本地后端
  frontend  前台启动本地前端
  status    查看容器、后端、前端状态
  logs      查看后端/前端最近日志
  steps     打印 Grok OAuth 手测步骤
  stop      停止后台后端/前端和本地 Postgres/Redis 容器

推荐给自动化/Claude 使用：
  tools/local-grok-oauth-test.sh up
  tools/local-grok-oauth-test.sh status
  tools/local-grok-oauth-test.sh logs
  tools/local-grok-oauth-test.sh stop

可选环境变量：
  POSTGRES_PORT=5432 REDIS_PORT=6379 SERVER_PORT=8080 FRONTEND_PORT=3000
  ADMIN_EMAIL=admin@local.test ADMIN_PASSWORD=12345678
EOF
    ;;
esac
