#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
把 grok_console 账号的 SSO 补票成 Grok Build OAuth 账号并导入指定分组。

背景
----
grok_session_credentials.encrypted_sso 里存的是 xAI 的 web SSO（JWT）。
同一份 SSO 既能走 console（DPoP），也能通过 device flow 铸造 Build OAuth
token —— 前提是补全 sub2api 早期实现漏掉的两步：
  1) approve 必须带 Origin/Referer（CSRF 校验），否则 403 "Request could not be verified"
  2) approve 必须回传 consent 页内嵌的 consent_token
本脚本直接调用已修复该流程的 sub2api 接口 POST /admin/grok/oauth/sso-token，
由服务端完成 device flow（也避免把 SSO 落到日志/磁盘）。

用法
----
  # 干跑：只统计与抽样，不建号
  python backfill_grok_build.py --dry-run --limit 5

  # 小批验证：处理 5 个
  python backfill_grok_build.py --limit 5

  # 全量
  python backfill_grok_build.py --all

前置
----
  - sub2api 已在 BASE 运行，且已加载与密文匹配的 TOTP_ENCRYPTION_KEY
  - 账号密码用于换 JWT，可走 --env-file 读取
"""

import argparse
import base64
import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

# ── 默认配置（可用命令行覆盖）──────────────────────────────────────────────
DEFAULT_BASE = "http://127.0.0.1:18080"
DEFAULT_ENV = r"C:\Users\70641\sub2api-native\runtime-env.json"
DEFAULT_SOURCE_GROUP = 31   # grok_console
DEFAULT_TARGET_GROUP = 2    # grok
DEFAULT_PROXY_ID = 1
AES_GCM_NONCE = 12
AES_GCM_TAG = 16


# ── HTTP ──────────────────────────────────────────────────────────────────
def http_json(method, url, token=None, body=None, timeout=120):
    data = None
    headers = {"Accept": "application/json"}
    if body is not None:
        data = json.dumps(body).encode("utf-8")
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, json.loads(resp.read().decode("utf-8") or "{}")
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", "replace")
        try:
            return e.code, json.loads(raw)
        except Exception:
            return e.code, {"raw": raw[:400]}
    except Exception as e:
        return 0, {"error": str(e)}


def login(base, email, password):
    status, body = http_json("POST", base + "/api/v1/auth/login",
                             body={"email": email, "password": password})
    if status != 200:
        raise SystemExit("登录失败 HTTP %s: %s" % (status, body))
    data = body.get("data") or {}
    token = data.get("token") or data.get("access_token")
    if not token:
        raise SystemExit("登录成功但未返回 token: %s" % list(data.keys()))
    return token


# ── 解密 SSO ──────────────────────────────────────────────────────────────
def load_key(env_path):
    with open(env_path, "r", encoding="utf-8") as f:
        env = json.load(f)
    key = (env.get("TOTP_ENCRYPTION_KEY") or "").strip()
    if len(key) != 64:
        raise SystemExit("TOTP_ENCRYPTION_KEY 必须是 64 位 hex（当前 %d 位）" % len(key))
    return bytes.fromhex(key)


def decrypt_sso(ciphertext_b64, key):
    """AES-256-GCM，格式 base64(nonce||ciphertext||tag)，与后端 aes_encryptor.go 一致。"""
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM  # 延迟导入

    blob = base64.b64decode(ciphertext_b64)
    if len(blob) < AES_GCM_NONCE + AES_GCM_TAG:
        raise ValueError("ciphertext too short")
    nonce, ct = blob[:AES_GCM_NONCE], blob[AES_GCM_NONCE:]
    return AESGCM(key).decrypt(nonce, ct, None).decode("utf-8")


# ── 代理池发现 ────────────────────────────────────────────────────────────
def discover_proxy_ids(psql, env, prefix):
    """按名称前缀查出当前存活的代理 ID。

    动态代理池（dpool-*）会周期性重建记录，ID 每几分钟就变，因此不能用
    命令行里写死的 ID，必须在每轮开始前重新发现。
    """
    sql = ("SELECT id FROM proxies WHERE status='active' AND deleted_at IS NULL "
           "AND name LIKE '%s%%' ORDER BY id" % prefix.replace("'", ""))
    out = subprocess.run([psql, "-h", "127.0.0.1", "-p", "15433", "-U", "sub2api",
                          "-d", "sub2api", "-A", "-t", "-c", sql],
                         capture_output=True, text=True, env=env)
    ids = []
    for line in out.stdout.splitlines():
        line = line.strip()
        if line.isdigit():
            ids.append(int(line))
    return ids


# ── 账号导入 ──────────────────────────────────────────────────────────────
def create_oauth_account(base, token, name, creds, group_ids, proxy_id):
    payload = {
        "name": name,
        "platform": "grok",
        "type": "oauth",
        "credentials": creds,
        "proxy_id": proxy_id,
        "group_ids": group_ids,
        "concurrency": 10,
    }
    return http_json("POST", base + "/api/v1/admin/accounts", token=token, body=payload)


def bulk_sso_to_oauth(base, token, sso_tokens, group_ids, proxy_id, timeout):
    """调用服务端批量补票接口：服务端并发跑 device flow，并按 group_ids 直接建号。"""
    payload = {
        "sso_tokens": sso_tokens,
        "group_ids": group_ids,
        "proxy_id": proxy_id,
        "concurrency": 10,
    }
    return http_json("POST", base + "/api/v1/admin/grok/sso-to-oauth",
                     token=token, body=payload, timeout=timeout)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default=DEFAULT_BASE)
    ap.add_argument("--env-file", default=DEFAULT_ENV)
    ap.add_argument("--target-group", type=int, default=DEFAULT_TARGET_GROUP)
    ap.add_argument("--proxy-id", type=int, default=DEFAULT_PROXY_ID)
    ap.add_argument("--proxy-ids", default="",
                    help="逗号分隔的代理 ID 列表；给出时按批轮换，用于分摊上游 429")
    ap.add_argument("--proxy-prefix", default="dpool-",
                    help="按名称前缀自动发现代理（动态池 ID 会变，每轮重新发现）")
    ap.add_argument("--limit", type=int, default=0, help="只处理前 N 个（0=全部）")
    ap.add_argument("--batch", type=int, default=30, help="每批提交给服务端的账号数")
    ap.add_argument("--workers", type=int, default=2, help="客户端并发批次数")
    ap.add_argument("--delay", type=float, default=1.5, help="同一 worker 内批间延迟（秒）")
    ap.add_argument("--retry-rounds", type=int, default=4, help="失败项重试轮数")
    ap.add_argument("--backoff", type=float, default=30, help="普通失败退避基数（秒）")
    ap.add_argument("--backoff-429", type=float, default=180, help="遇 429 时的退避基数（秒）")
    ap.add_argument("--all", action="store_true", help="确认全量处理")
    ap.add_argument("--dry-run", action="store_true")
    ap.add_argument("--admin-email", default="admin@local.test")
    ap.add_argument("--admin-password", default="12345678")
    args = ap.parse_args()

    if not args.dry_run and not args.limit and not args.all:
        raise SystemExit("拒绝无限量执行：请加 --limit N 或 --all 明确范围")

    key = load_key(args.env_file)
    token = login(args.base, args.admin_email, args.admin_password)

    # 取待处理账号：grok_console + 有 SSO 密文
    import subprocess
    psql = r"C:\Users\70641\sub2api-native\postgres\bin\psql.exe"
    env = dict(os.environ, PGPASSWORD="local_sub2api_password")
    sql = (
        "SELECT a.id || '|' || a.name || '|' || g.encrypted_sso "
        "FROM accounts a JOIN grok_session_credentials g ON g.account_id = a.id "
        "WHERE a.type='grok_console' AND a.deleted_at IS NULL "
        "AND g.encrypted_sso IS NOT NULL AND g.encrypted_sso <> '' "
        "ORDER BY a.id"
    )
    out = subprocess.run([psql, "-h", "127.0.0.1", "-p", "15433", "-U", "sub2api",
                          "-d", "sub2api", "-A", "-t", "-c", sql],
                         capture_output=True, text=True, env=env)
    if out.returncode != 0:
        raise SystemExit("查询账号失败: " + out.stderr[:300])

    # 去重：只按「仍存活」的 grok oauth 账号跳过，避免重跑时重复建号。
    # 注意必须带 deleted_at IS NULL：库里 6000+ 条已删除的历史 oauth 记录，
    # 若把它们也算作「已存在」，会把几乎所有待补票账号误判为重复而全部跳过。
    sql_existing = (
        "SELECT lower(coalesce(credentials->>'email','')), lower(name) FROM accounts "
        "WHERE platform='grok' AND type='oauth' AND deleted_at IS NULL"
    )
    out2 = subprocess.run([psql, "-h", "127.0.0.1", "-p", "15433", "-U", "sub2api",
                           "-d", "sub2api", "-A", "-t", "-c", sql_existing],
                          capture_output=True, text=True, env=env)
    existing_emails = set()
    for line in out2.stdout.splitlines():
        for field in line.split("|"):
            field = field.strip().lower()
            if field:
                existing_emails.add(field)
    print("已存在 grok oauth（存活）标识: %d" % len(existing_emails))

    rows = [ln for ln in out.stdout.splitlines() if ln.strip()]
    print("待处理账号: %d" % len(rows))

    # 先本地解密（失败的直接跳过并报告），再把 SSO 交给服务端批量补票。
    # 只解密、不外传密文；明文仅存在于本进程内存与请求体。
    decrypted = []   # (aid, name, sso)
    decrypt_failed = 0
    skipped_dup = 0
    for i, row in enumerate(rows, 1):
        parts = row.split("|", 2)
        if len(parts) != 3:
            continue
        aid, name, enc = parts
        if name.strip().lower() in existing_emails:
            skipped_dup += 1
            continue
        # --limit 必须作用在「去重后」的列表上，否则前 N 行大多是已有账号，
        # 实际只会处理极少数（甚至 1 个）。
        if args.limit and len(decrypted) >= args.limit:
            break
        try:
            decrypted.append((aid, name, decrypt_sso(enc, key)))
        except Exception as e:
            decrypt_failed += 1
            print("[%d/%d] %s 解密失败: %s" % (i, len(rows), aid, e))

    print("解密成功 %d / 失败 %d / 跳过重复 %d" % (len(decrypted), decrypt_failed, skipped_dup))
    if args.dry_run:
        print("[dry-run] 不建号。")
        return 0

    if not decrypted:
        print("没有可补票的账号")
        return 1

    # 服务端每个请求内部并发固定 3（grokSSOImportConcurrency），单批吞吐受限。
    # 这里由客户端并发发起多个批次请求，整体吞吐 ≈ 3 * workers。
    # 注意：所有请求共用同一个 SSO 出口代理，并发过高会被上游 429，
    # 因此 workers 默认保守，且失败项按轮重试 + 指数退避。
    import concurrent.futures

    batch_size = max(1, args.batch)
    workers = max(1, args.workers)
    per_batch_timeout = max(300, ((batch_size + 2) // 3) * 90 + 60)

    ok = 0
    pending = list(decrypted)
    total_initial = len(pending)
    fixed_proxy_ids = [int(x) for x in args.proxy_ids.split(",") if x.strip()]

    def current_proxy_ids():
        if fixed_proxy_ids:
            return fixed_proxy_ids
        return discover_proxy_ids(psql, env, args.proxy_prefix) or [args.proxy_id]

    def run_batch(idx_chunk):
        idx, chunk, proxy_ids = idx_chunk
        if args.delay > 0:
            time.sleep(args.delay)
        # 按批号轮换出口 IP，避免单一出口触发上游 429。
        pid = proxy_ids[idx % len(proxy_ids)]
        tokens = [c[2] for c in chunk]
        status, body = bulk_sso_to_oauth(args.base, token, tokens,
                                         [args.target_group], pid,
                                         per_batch_timeout)
        if status != 200 or body.get("code") != 0:
            return idx, chunk, "HTTP %s: %s" % (status, str(body.get("message") or body)[:160]), None
        return idx, chunk, None, (body.get("data") or {})

    for rnd in range(1, args.retry_rounds + 1):
        if not pending:
            break
        chunks = [pending[s:s + batch_size] for s in range(0, len(pending), batch_size)]
        proxy_ids = current_proxy_ids()
        print("\n=== 第 %d 轮：%d 项 / %d 批 / 并发 %d / 出口 %d 个 ==="
              % (rnd, len(pending), len(chunks), workers, len(proxy_ids)))

        retry_items = []
        rate_limited = 0
        with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
            futures = [pool.submit(run_batch, (i, c, proxy_ids))
                       for i, c in enumerate(chunks)]
            done = 0
            for fut in concurrent.futures.as_completed(futures):
                done += 1
                try:
                    idx, chunk, err, data = fut.result()
                except Exception as e:
                    retry_items.extend(chunk)
                    print("[%d/%d] 批次异常，整批转入重试: %s" % (done, len(chunks), e))
                    continue
                if err:
                    retry_items.extend(chunk)
                    print("[%d/%d] 批 %d 整体失败，转入重试: %s" % (done, len(chunks), idx + 1, err))
                    continue
                created = data.get("created") or []
                failed = data.get("failed") or []
                ok += len(created)
                for item in failed:
                    pos = item.get("index")
                    if isinstance(pos, int) and 1 <= pos <= len(chunk):
                        retry_items.append(chunk[pos - 1])
                    if "429" in str(item.get("error") or ""):
                        rate_limited += 1
                print("[%d/%d] 批 %d: 成功 %d / 失败 %d（累计成功 %d）"
                      % (done, len(chunks), idx + 1, len(created), len(failed), ok))

        pending = retry_items
        print("第 %d 轮结束：累计成功 %d / 待重试 %d（其中 429 报错 %d）"
              % (rnd, ok, len(pending), rate_limited))
        if not pending or rnd >= args.retry_rounds:
            break
        base = args.backoff_429 if rate_limited else args.backoff
        wait = min(base * (2 ** (rnd - 1)), 900)
        print("退避 %.0fs 后进入第 %d 轮..." % (wait, rnd + 1))
        time.sleep(wait)

    fail = len(pending)
    print("\n完成: 成功 %d / 失败 %d / 解密失败 %d（初始 %d 项）"
          % (ok, fail, decrypt_failed, total_initial))
    return 0 if fail == 0 and decrypt_failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
