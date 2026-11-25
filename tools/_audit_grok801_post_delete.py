#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Audit remaining grok801 (group 11) accounts after batch delete."""
from __future__ import annotations

import json
import urllib.error
import urllib.request
from collections import Counter
from pathlib import Path

BASE = "http://127.0.0.1:18080"
OUT = Path("/tmp/grok801_post_delete_audit.json")
LOG = Path("/tmp/grok801_post_delete_audit.log")
GROUP_ID = 11
DELETED_RANGE = range(2031, 2437)


def log(msg: str) -> None:
    line = msg if msg.endswith("\n") else msg + "\n"
    print(msg, flush=True)
    with LOG.open("a", encoding="utf-8") as f:
        f.write(line)


def req(method, path, data=None, token=None, timeout=60):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = f"Bearer {token}"
    body = None if data is None else json.dumps(data).encode()
    r = urllib.request.Request(BASE + path, data=body, headers=h, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, str(e)


def main():
    LOG.write_text("", encoding="utf-8")
    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    if st != 200:
        log(f"login_fail {st} {raw[:300]}")
        raise SystemExit(1)
    token = json.loads(raw)["data"]["access_token"]
    log("login ok")

    st, raw = req("GET", f"/api/v1/admin/groups/{GROUP_ID}", token=token)
    g = json.loads(raw)["data"]
    log(f"group name={g.get('name')} account_count={g.get('account_count')} status={g.get('status')}")

    # List with group_id filter (may be unreliable for total; still collect pages)
    listed = []
    page = 1
    claimed_total = None
    while page <= 30:
        st, raw = req(
            "GET",
            f"/api/v1/admin/accounts?page={page}&page_size=100&group_id={GROUP_ID}&sort_by=id&sort_order=asc",
            token=token,
        )
        if st != 200:
            log(f"list_fail page={page} {st} {raw[:200]}")
            break
        data = json.loads(raw).get("data")
        if isinstance(data, dict):
            batch = data.get("items") or data.get("accounts") or data.get("list") or []
            claimed_total = data.get("total")
        elif isinstance(data, list):
            batch = data
        else:
            batch = []
        if not batch:
            break
        listed.extend(batch)
        if claimed_total is not None and len(listed) >= claimed_total:
            break
        if len(batch) < 100:
            break
        page += 1

    log(f"list_len={len(listed)} claimed_total={claimed_total} pages={page}")
    if listed:
        log(f"list status Counter={dict(Counter(a.get('status') for a in listed))}")
        log(f"list id range {listed[0].get('id')}..{listed[-1].get('id')}")

    # Detail every listed id + also scan likely remaining by known pre-batch ids if needed
    # Prefer membership from detail group_ids
    members = []
    get_fail = []
    for i, a in enumerate(listed, 1):
        aid = a["id"]
        st, raw = req("GET", f"/api/v1/admin/accounts/{aid}", token=token)
        if st != 200:
            get_fail.append({"id": aid, "http": st, "body": raw[:120]})
            continue
        acc = json.loads(raw)["data"]
        gids = acc.get("group_ids") or []
        cs = acc.get("credentials_status") or {}
        creds = acc.get("credentials") or {}
        err = acc.get("error_message") or acc.get("error") or ""
        row = {
            "id": acc["id"],
            "name": acc.get("name"),
            "status": acc.get("status"),
            "error": str(err)[:160],
            "created_at": str(acc.get("created_at") or "")[:19],
            "group_ids": gids,
            "in_g11": GROUP_ID in gids,
            "schedulable": acc.get("schedulable"),
            "proxy_id": acc.get("proxy_id"),
            "has_access_flag": bool(isinstance(cs, dict) and cs.get("has_access_token")),
            "has_refresh_flag": bool(isinstance(cs, dict) and cs.get("has_refresh_token")),
            "cred_keys": sorted(creds.keys()) if isinstance(creds, dict) else [],
            "cred_key_count": len(creds) if isinstance(creds, dict) else -1,
            "access_present": bool(isinstance(creds, dict) and creds.get("access_token")),
            "refresh_present": bool(isinstance(creds, dict) and creds.get("refresh_token")),
        }
        if row["in_g11"]:
            members.append(row)
        if i % 25 == 0 or i == len(listed):
            log(f"detail progress {i}/{len(listed)} members={len(members)} get_fail={len(get_fail)}")

    # Also check none of deleted range still exists
    still_deleted_range = []
    for aid in list(DELETED_RANGE)[::50]:  # sample every 50
        st, raw = req("GET", f"/api/v1/admin/accounts/{aid}", token=token)
        if st == 200:
            still_deleted_range.append(aid)

    status_c = Counter(m["status"] for m in members)
    err_c = Counter()
    for m in members:
        if m["status"] != "active" or m["error"]:
            key = f"{m['status']}|{m['error'][:80]}"
            err_c[key] += 1

    no_access = [m for m in members if not m["access_present"] and not m["has_access_flag"]]
    no_refresh = [m for m in members if not m["refresh_present"] and not m["has_refresh_flag"]]
    error_status = [m for m in members if m["status"] == "error" or "invalid" in m["error"].lower() or "凭证" in m["error"] or "credential" in m["error"].lower() or "token" in m["error"].lower()]

    log(f"true_members={len(members)}")
    log(f"status={dict(status_c)}")
    log(f"no_access={len(no_access)} no_refresh={len(no_refresh)} errorish={len(error_status)}")
    log("error buckets:")
    for k, v in err_c.most_common(20):
        log(f"  {v} {k}")
    log(f"created_prefix={Counter(m['created_at'][:13] for m in members).most_common(15)}")
    log(f"deleted_range_still_present_sample={still_deleted_range}")

    # Print all errorish ids for user
    for m in error_status[:40]:
        log(f"ERR id={m['id']} status={m['status']} created={m['created_at']} proxy={m['proxy_id']} err={m['error']!r} cred_keys={m['cred_keys']}")

    out = {
        "group_account_count": g.get("account_count"),
        "list_len": len(listed),
        "true_members": len(members),
        "status": dict(status_c),
        "no_access_ids": [m["id"] for m in no_access],
        "no_refresh_ids": [m["id"] for m in no_refresh],
        "errorish": [
            {
                "id": m["id"],
                "status": m["status"],
                "error": m["error"],
                "created_at": m["created_at"],
                "proxy_id": m["proxy_id"],
                "cred_keys": m["cred_keys"],
                "access_present": m["access_present"],
                "refresh_present": m["refresh_present"],
            }
            for m in error_status
        ],
        "all_members": members,
        "get_fail": get_fail,
    }
    OUT.write_text(json.dumps(out, ensure_ascii=False, indent=2), encoding="utf-8")
    log(f"wrote {OUT}")


if __name__ == "__main__":
    main()
