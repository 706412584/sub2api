#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Trial: convert one error account's extra.sso via admin sso-to-oauth (creates NEW account)."""
from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from pathlib import Path

BASE = "http://127.0.0.1:18080"
SOURCE_ID = 1949
# US3 more reliable for xAI than local-7887; original error account used proxy 1
PROXY_ID = 5
OUT = Path("/tmp/sso_trial_convert_result.json")
# no group bind — temporary trial account only
GROUP_IDS: list[int] = []


def req(method, path, data=None, token=None, timeout=180):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = f"Bearer {token}"
    body = None if data is None else json.dumps(data, ensure_ascii=False).encode()
    r = urllib.request.Request(BASE + path, data=body, headers=h, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, str(e)


def redact_account(acc: dict | None) -> dict | None:
    if not acc:
        return None
    creds = acc.get("credentials") or {}
    extra = acc.get("extra") or {}
    cs = acc.get("credentials_status") or {}
    return {
        "id": acc.get("id"),
        "name": acc.get("name"),
        "status": acc.get("status"),
        "error": (acc.get("error_message") or acc.get("error") or "")[:160],
        "proxy_id": acc.get("proxy_id"),
        "group_ids": acc.get("group_ids"),
        "created_at": str(acc.get("created_at") or "")[:19],
        "cred_keys": sorted(creds.keys()) if isinstance(creds, dict) else [],
        "has_access_token": bool(isinstance(creds, dict) and creds.get("access_token")),
        "has_refresh_token": bool(isinstance(creds, dict) and creds.get("refresh_token")),
        "has_id_token": bool(isinstance(creds, dict) and creds.get("id_token")),
        "expires_at": creds.get("expires_at") if isinstance(creds, dict) else None,
        "email_cred": creds.get("email") if isinstance(creds, dict) else None,
        "token_type": creds.get("token_type") if isinstance(creds, dict) else None,
        "access_len": len(creds["access_token"]) if isinstance(creds, dict) and isinstance(creds.get("access_token"), str) else 0,
        "refresh_len": len(creds["refresh_token"]) if isinstance(creds, dict) and isinstance(creds.get("refresh_token"), str) else 0,
        "cs": cs if isinstance(cs, dict) else {},
        "extra_keys": sorted(extra.keys()) if isinstance(extra, dict) else [],
        "extra_has_sso": bool(isinstance(extra, dict) and extra.get("sso")),
    }


def main():
    t0 = time.time()
    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    if st != 200:
        print("login_fail", st, raw[:200])
        raise SystemExit(1)
    token = json.loads(raw)["data"]["access_token"]
    print("login ok", flush=True)

    st, raw = req("GET", f"/api/v1/admin/accounts/{SOURCE_ID}", token=token)
    if st != 200:
        print("get_source_fail", st, raw[:200])
        raise SystemExit(1)
    src = json.loads(raw)["data"]
    sso = (src.get("extra") or {}).get("sso")
    if not sso or not isinstance(sso, str):
        print("no sso on source")
        raise SystemExit(1)
    print(
        f"source id={SOURCE_ID} status={src.get('status')} proxy={src.get('proxy_id')} "
        f"sso_len={len(sso)} email={(src.get('extra') or {}).get('email')}",
        flush=True,
    )
    print(f"calling POST /api/v1/admin/grok/sso-to-oauth proxy_id={PROXY_ID} ...", flush=True)

    payload = {
        "sso_tokens": [sso],
        "name": f"sso-trial-from-{SOURCE_ID}",
        "proxy_id": PROXY_ID,
        "group_ids": GROUP_IDS,
        "concurrency": 1,
        # keep sso in extra on new account for traceability
        "extra": {
            "email": (src.get("extra") or {}).get("email"),
            "sso": sso,
            "sso_trial_source_account_id": SOURCE_ID,
        },
    }
    st, raw = req(
        "POST",
        "/api/v1/admin/grok/sso-to-oauth",
        payload,
        token=token,
        timeout=180,
    )
    elapsed = round(time.time() - t0, 1)
    print(f"http={st} elapsed_s={elapsed}", flush=True)

    result = {
        "source_id": SOURCE_ID,
        "proxy_id_used": PROXY_ID,
        "http_status": st,
        "elapsed_s": elapsed,
        "raw_snip": raw[:500] if st != 200 else None,
    }

    if st != 200:
        result["ok"] = False
        result["error"] = raw[:800]
        OUT.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
        print("FAIL", raw[:400])
        print("wrote", OUT)
        return

    data = json.loads(raw).get("data") or {}
    created = data.get("created") or []
    failed = data.get("failed") or []
    result["created_count"] = len(created)
    result["failed_count"] = len(failed)
    result["failed"] = [
        {"index": f.get("index"), "error": (f.get("error") or "")[:300], "email": f.get("email"), "name": f.get("name")}
        for f in failed
    ]

    if failed and not created:
        result["ok"] = False
        OUT.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
        print("CONVERT_FAILED", result["failed"])
        print("wrote", OUT)
        return

    # inspect created account(s) — full GET for credentials presence
    created_summaries = []
    for item in created:
        acc_brief = item.get("account") or {}
        new_id = acc_brief.get("id")
        summary = {
            "index": item.get("index"),
            "name": item.get("name"),
            "email": item.get("email"),
            "response_account": redact_account(acc_brief) if acc_brief else None,
        }
        if new_id:
            st2, raw2 = req("GET", f"/api/v1/admin/accounts/{new_id}", token=token)
            if st2 == 200:
                full = json.loads(raw2)["data"]
                summary["full"] = redact_account(full)
            else:
                summary["full_http"] = st2
                summary["full_err"] = raw2[:200]
        created_summaries.append(summary)

    result["ok"] = True
    result["created"] = created_summaries
    # got fresh tokens?
    got = False
    for s in created_summaries:
        full = s.get("full") or s.get("response_account") or {}
        if full.get("has_access_token") and full.get("has_refresh_token"):
            got = True
    result["got_access_and_refresh"] = got

    OUT.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print("ok=", result["ok"], "got_access_and_refresh=", got)
    print(json.dumps({k: result[k] for k in result if k != "created"}, ensure_ascii=False, indent=2))
    for s in created_summaries:
        print("created:", json.dumps(s, ensure_ascii=False))
    print("wrote", OUT)


if __name__ == "__main__":
    main()
