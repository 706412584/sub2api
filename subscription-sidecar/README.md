# Sub2API Subscription Sidecar A

独立进程：拉取 Clash 订阅 → 选出 N 个节点 → 在本机为每个节点开 mixed 监听端口 → 用 Admin API 写入/更新 Sub2API `proxies`（**不自动绑定账号/分组**）。

## 能力边界

| 支持 | 不支持 |
|------|--------|
| Clash YAML / base64 YAML 订阅 | 直接把订阅 URL 塞进 Sub2API 核心 |
| 多本地端口（每节点一端口） | 账号/分组自动 `proxy_id` 绑定 |
| `http/https/socks5/socks5h` 写入 Sub2API | 把 VMess/VLESS 节点 host 直接当 proxy |
| prune 未绑定账号的过期 sidecar proxy | FlClash GUI |

实际协议节点由本机 **mihomo** 承接；Sub2API 只连 `127.0.0.1:<port>`。

## 快速开始

```bash
cd subscription-sidecar
go test ./...
go build -o subscription-sidecar ./cmd/subscription-sidecar

export SIDECAR_SUBSCRIPTION_FILE=./testdata/subscription-sample.yaml
export SIDECAR_DRY_RUN=1
export SIDECAR_SKIP_ENGINE=1
export SIDECAR_ONCE=1
./subscription-sidecar
```

生产（示例）：

```bash
export SIDECAR_SUBSCRIPTION_URL='<your private subscription url>'
export SIDECAR_ADMIN_API_KEY='admin-...'
export SIDECAR_SUB2API_BASE_URL='http://127.0.0.1:18080'
export SIDECAR_MAX_PORTS=10
./subscription-sidecar
```

## 环境变量

见 `deploy/subscription-sidecar.env.example`。

关键点：

- `SIDECAR_SUBSCRIPTION_URL` 或 `SIDECAR_SUBSCRIPTION_FILE` **必填其一**（无默认订阅，避免泄露）
- `SIDECAR_ADMIN_API_KEY`：非 dry-run 必填
- `SIDECAR_NAME_PREFIX` 必须以 `sidecar-` 开头（默认 `sidecar-a-`）
- 写入的 proxy：`host=SIDECAR_BIND_ADDRESS`，`port=BASE_PORT + index`

## 与 Sub2API 的关系

1. 侧车 `POST/PUT/DELETE /api/v1/admin/proxies*`
2. 运维在管理后台把账号/分组手动绑到对应 `proxy_id`
3. 请求链路：Sub2API → `127.0.0.1:port` → mihomo 选中节点 → 上游

## 部署

- Linux：`deploy/sub2api-subscription-sidecar.service`
- Windows：与 `sub2api-native` 并列运行；可用 `SIDECAR_ONCE=1` 做定时任务

## 安全

- 不要把真实订阅 URL / admin key 提交进 git
- 监听默认仅 `127.0.0.1`（非 loopback 需 `SIDECAR_ALLOW_NON_LOCAL_BIND=1`）
- 带 `account_count>0` 的 sidecar proxy **不会被自动删除**
- 分组 `default_proxy_id` 目前不计入 account_count；请勿把分组默认代理绑到可能被 prune 的 sidecar 名，或先解绑再删
- Sidecar 管理的 proxy **不要配置 fallback/expiry**（Sub2API Update 会用零值覆盖这些字段）
- mihomo 仅在生成配置 hash 变化时重启
