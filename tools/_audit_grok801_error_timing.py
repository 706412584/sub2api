#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Deep-check remaining grok801 members: error timing vs delete batch."""
from __future__ import annotations

import json
import urllib.error
import urllib.request
from collections import Counter
from pathlib import Path

BASE = "http://127.0.0.1:18080"
AUDIT = Path("/tmp/grok801_post_delete_audit.json")
OUT = Path("/tmp/grok801_error_timing.json")


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
    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    token = json.loads(raw)["data"]["access_token"]
    audit = json.loads(AUDIT.read_text(encoding="utf-8"))
    members = audit["all_members"]
    print("members", len(members), "status", audit["status"])

    rows = []
    for m in members:
        st, raw = req("GET", f"/api/v1/admin/accounts/{m['id']}", token=token)
        if st != 200:
            rows.append({"id": m["id"], "http": st})
            continue
        acc = json.loads(raw)["data"]
        creds = acc.get("credentials") or {}
        cs = acc.get("credentials_status") or {}
        extra = acc.get("extra") or {}
        exp = str(creds.get("expires_at") or "")
        rows.append(
            {
                "id": acc["id"],
                "status": acc.get("status"),
                "created_at": str(acc.get("created_at") or ""),
                "updated_at": str(acc.get("updated_at") or ""),
                "error": str(acc.get("error_message") or acc.get("error") or "")[:200],
                "proxy_id": acc.get("proxy_id"),
                "schedulable": acc.get("schedulable"),
                "group_ids": acc.get("group_ids"),
                "expires_at": exp,
                "cred_keys": sorted(creds.keys()) if isinstance(creds, dict) else [],
                "has_access_token_key": "access_token" in (creds or {}),
                "has_refresh_token_key": "refresh_token" in (creds or {}),
                "cs": cs,
                "email": creds.get("email") if isinstance(creds, dict) else None,
                "extra_keys": sorted(extra.keys()) if isinstance(extra, dict) else [],
                "name": acc.get("name"),
            }
        )

    active = [r for r in rows if r.get("status") == "active"]
    err = [r for r in rows if r.get("status") == "error"]
    print("active", len(active), "error", len(err))
    print("error created_at", Counter(r["created_at"][:19] for r in err).most_common())
    print("error updated_at", Counter(r["updated_at"][:19] for r in err).most_common(20))
    print("active created_at", Counter(r["created_at"][:19] for r in active).most_common())
    print("active updated_at", Counter(r["updated_at"][:19] for r in active).most_common(10))
    print(
        "error has_access_key",
        Counter(r.get("has_access_token_key") for r in err),
        "has_refresh_key",
        Counter(r.get("has_refresh_token_key") for r in err),
    )
    print(
        "active has_access_key",
        Counter(r.get("has_access_token_key") for r in active),
        "has_refresh_key",
        Counter(r.get("has_refresh_token_key") for r in active),
    )
    print("error expires_at sample", Counter(r.get("expires_at") for r in err).most_common(10))
    print("active expires_at sample", Counter((r.get("expires_at") or "")[:20] for r in active).most_common(10))
    print("error proxy", Counter(r.get("proxy_id") for r in err))
    print("active proxy", Counter(r.get("proxy_id") for r in active))
    print("error id range", min(r["id"] for r in err), max(r["id"] for r in err) if err else None)
    print("active id range", min(r["id"] for r in active), max(r["id"] for r in active) if active else None)
    # overlap with deleted batch?
    deleted = set(range(2031, 2437))
    print("error in deleted id range", sum(1 for r in err if r["id"] in deleted))
    print("active in deleted id range", sum(1 for r in active if r["id"] in deleted))
    print("any member in deleted range", sum(1 for r in rows if r.get("id") in deleted))

    # compare updated_at vs our delete window (approx now session) — just report times
    for r in err[:5]:
        print("ERR sample", {k: r[k] for k in ("id", "created_at", "updated_at", "expires_at", "has_access_token_key", "has_refresh_token_key", "proxy_id")})
    for r in active[:5]:
        print("OK sample", {k: r[k] for k in ("id", "created_at", "updated_at", "expires_at", "has_access_token_key", "has_refresh_token_key", "proxy_id")})

    out = {
        "active_count": len(active),
        "error_count": len(err),
        "error_ids": [r["id"] for r in err],
        "active_ids": [r["id"] for r in active],
        "error_updated_at": dict(Counter(r["updated_at"][:19] for r in err)),
        "error_created_at": dict(Counter(r["created_at"][:19] for r in err)),
        "rows": rows,
    }
    OUT.write_text(json.dumps(out, ensure_ascii=False, indent=2), encoding="utf-8")
    print("wrote", OUT)


if __name__ == "__main__":
    main()
