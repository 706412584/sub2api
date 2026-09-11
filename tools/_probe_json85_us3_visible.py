#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Import JSON Grok accounts (if missing) and probe visible reasoning via US3 proxy=5."""
from __future__ import annotations

import json
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from pathlib import Path

BASE = "http://127.0.0.1:18080"
JSON_PATH = Path(r"D:\download\grok_sub2api_20260802_212936最新.json")
OUT = Path("/tmp/json85_us3_visible_probe.json")
PROXY_ID = 5  # 美国3
PROBE_GAP = 0.35


def req(method, path, data=None, token=None, timeout=120):
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    body = None if data is None else json.dumps(data, ensure_ascii=False).encode("utf-8")
    r = urllib.request.Request(BASE + path, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, str(e)


def safe(s, n=200):
    return (s or "")[:n].encode("unicode_escape").decode("ascii")


def jload(raw):
    return json.loads(raw)


def account_email(item: dict) -> str:
    extra = item.get("extra") or {}
    cred = item.get("credentials") or {}
    return (
        (extra.get("email") if isinstance(extra, dict) else None)
        or (cred.get("email") if isinstance(cred, dict) else None)
        or item.get("name")
        or ""
    )


def find_existing(token: str, email: str):
    q = urllib.parse.quote(email)
    st, raw = req(
        "GET",
        f"/api/v1/admin/accounts?search={q}&page_size=20&platform=grok",
        token=token,
    )
    if st != 200:
        return None, st, raw
    items = (jload(raw).get("data") or {}).get("items") or []
    for a in items:
        extra = a.get("extra") or {}
        cred = a.get("credentials") or {}
        names = {
            a.get("name"),
            extra.get("email") if isinstance(extra, dict) else None,
            cred.get("email") if isinstance(cred, dict) else None,
        }
        if email in names:
            return a, st, raw
    # fallback: exact name match only
    for a in items:
        if a.get("name") == email:
            return a, st, raw
    return None, st, raw


def main():
    payload = jload(JSON_PATH.read_text(encoding="utf-8"))
    accounts = payload.get("accounts") or []
    print(f"json accounts={len(accounts)} path={JSON_PATH}")

    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    if st != 200:
        raise SystemExit(f"login failed {st} {safe(raw)}")
    token = jload(raw)["data"]["access_token"]

    # confirm proxy 5
    st, raw = req("GET", f"/api/v1/admin/proxies/{PROXY_ID}", token=token)
    if st != 200:
        raise SystemExit(f"proxy {PROXY_ID} missing: {st} {safe(raw)}")
    proxy = jload(raw).get("data") or {}
    print(f"proxy {PROXY_ID} name={proxy.get('name')} host={proxy.get('host')} status={proxy.get('status')}")

    results = {
        "proxy_id": PROXY_ID,
        "proxy_name": proxy.get("name"),
        "import": {"created": 0, "reused": 0, "failed": []},
        "account_map": [],  # email -> id
        "probes": [],
        "summary": {},
    }

    # Import missing accounts bound to US3
    id_by_email = {}
    to_import = []
    for item in accounts:
        email = account_email(item)
        existing, st, raw = find_existing(token, email)
        if existing:
            id_by_email[email] = existing["id"]
            results["import"]["reused"] += 1
            results["account_map"].append(
                {
                    "email": email,
                    "account_id": existing["id"],
                    "source": "existing",
                    "proxy_id": existing.get("proxy_id"),
                }
            )
        else:
            to_import.append(item)

    print(f"existing={results['import']['reused']} to_import={len(to_import)}")

    if to_import:
        # batch import via /admin/accounts/data with proxy_id override
        # chunk to avoid huge body / timeout
        chunk_size = 20
        for i in range(0, len(to_import), chunk_size):
            chunk = to_import[i : i + chunk_size]
            body = {
                "data": {
                    "type": payload.get("type") or "sub2api-data",
                    "version": payload.get("version") or 1,
                    "proxies": [],
                    "accounts": chunk,
                },
                "skip_default_group_bind": True,
                "proxy_id": PROXY_ID,
            }
            t0 = time.time()
            st, raw = req("POST", "/api/v1/admin/accounts/data", body, token=token, timeout=180)
            dt = round(time.time() - t0, 2)
            if st != 200:
                print(f"import chunk {i} failed http={st} {safe(raw)}")
                results["import"]["failed"].append({"offset": i, "http": st, "err": safe(raw)})
                continue
            data = jload(raw).get("data") or jload(raw)
            created = data.get("account_created") or 0
            failed = data.get("account_failed") or 0
            results["import"]["created"] += created
            print(
                f"import chunk offset={i} n={len(chunk)} created={created} failed={failed} dt={dt} errors={len(data.get('errors') or [])}"
            )
            for err in (data.get("errors") or [])[:5]:
                print("  err", err)
                results["import"]["failed"].append(err)
            time.sleep(0.3)

        # resolve ids for newly imported
        for item in to_import:
            email = account_email(item)
            if email in id_by_email:
                continue
            existing, st, raw = find_existing(token, email)
            if existing:
                id_by_email[email] = existing["id"]
                results["account_map"].append(
                    {
                        "email": email,
                        "account_id": existing["id"],
                        "source": "imported",
                        "proxy_id": existing.get("proxy_id"),
                    }
                )
            else:
                results["import"]["failed"].append({"email": email, "err": "not_found_after_import"})
                print("missing after import", email)

    print(f"mapped accounts={len(id_by_email)}/{len(accounts)}")

    # Ensure proxy_id=5 for all mapped (import override may set; existing may differ)
    for email, aid in list(id_by_email.items()):
        acc_st, acc_raw = req("GET", f"/api/v1/admin/accounts/{aid}", token=token)
        if acc_st != 200:
            continue
        acc = jload(acc_raw).get("data") or {}
        if acc.get("proxy_id") != PROXY_ID:
            st, raw = req(
                "PUT",
                f"/api/v1/admin/accounts/{aid}",
                {"proxy_id": PROXY_ID},
                token=token,
            )
            print(f"set proxy {aid} -> {PROXY_ID} http={st}")

    # Probe all via US3
    print(f"\n=== PROBES proxy={PROXY_ID} n={len(id_by_email)} ===")
    status_counter = Counter()
    visible_ids = []
    for idx, item in enumerate(accounts, 1):
        email = account_email(item)
        aid = id_by_email.get(email)
        if not aid:
            row = {"email": email, "status": "missing_account"}
            results["probes"].append(row)
            status_counter["missing_account"] += 1
            print(f"[{idx}/{len(accounts)}] MISSING {email}")
            continue
        t0 = time.time()
        st, raw = req(
            "POST",
            f"/api/v1/admin/proxies/{PROXY_ID}/grok-reasoning-probe",
            {"account_id": aid, "confirm_quota_cost": True},
            token=token,
            timeout=120,
        )
        dt = round(time.time() - t0, 2)
        row = {"i": idx, "email": email, "account_id": aid, "http": st, "dt": dt}
        if st == 200:
            data = jload(raw).get("data") or jload(raw)
            if isinstance(data, dict):
                for k in [
                    "status",
                    "has_visible_reasoning",
                    "visible_reasoning_chars",
                    "has_encrypted_reasoning",
                    "reasoning_tokens",
                    "http_status",
                    "message",
                    "latency_ms",
                    "stream_completed",
                ]:
                    if k in data:
                        row[k] = data[k]
                row["visible"] = bool(data.get("has_visible_reasoning"))
                status_counter[str(data.get("status") or "unknown")] += 1
                if row["visible"]:
                    visible_ids.append(aid)
        else:
            row["err"] = safe(raw)
            status_counter[f"http_{st}"] += 1
        results["probes"].append(row)
        print(
            f"[{idx}/{len(accounts)}] id={aid} status={row.get('status')} "
            f"vis={row.get('visible')} chars={row.get('visible_reasoning_chars')} "
            f"rtok={row.get('reasoning_tokens')} dt={dt} msg={safe(str(row.get('message') or row.get('err') or ''), 70)}"
        )
        time.sleep(PROBE_GAP)

    ok = [p for p in results["probes"] if p.get("http") == 200]
    vis = [p for p in ok if p.get("visible")]
    summary = {
        "total_json": len(accounts),
        "mapped": len(id_by_email),
        "import_created": results["import"]["created"],
        "import_reused": results["import"]["reused"],
        "probe_http_ok": len(ok),
        "visible": f"{len(vis)}/{len(ok)}",
        "visible_account_ids": visible_ids,
        "status_counts": dict(status_counter),
    }
    results["summary"] = summary
    OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
    print("\nSUMMARY", json.dumps(summary, ensure_ascii=False))
    print("saved", OUT)


if __name__ == "__main__":
    main()
