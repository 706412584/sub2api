#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Fix bogus expires_at=0 on imported JSON85 accounts, then probe US3 visible reasoning."""
from __future__ import annotations

import base64
import json
import time
import urllib.error
import urllib.request
from collections import Counter
from pathlib import Path

BASE = "http://127.0.0.1:18080"
SRC = Path(r"D:\download\grok_sub2api_20260802_212936最新.json")
OUT = Path("/tmp/json85_us3_visible_probe_fixed_exp.json")
PROXY_ID = 5


def req(method, path, data=None, token=None, timeout=120):
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


def safe(s, n=200):
    return (s or "")[:n].encode("unicode_escape").decode("ascii")


def jwt_payload(tok: str) -> dict:
    try:
        p = tok.split(".")[1]
        p += "=" * ((4 - len(p) % 4) % 4)
        return json.loads(base64.urlsafe_b64decode(p))
    except Exception:
        return {}


def to_expires_at(cred: dict) -> str:
    exp = jwt_payload(cred.get("access_token") or "").get("exp")
    if exp and exp > time.time():
        return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(exp))
    return str(cred.get("expires_at") or "")


def main():
    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    token = json.loads(raw)["data"]["access_token"]
    src_accounts = json.loads(SRC.read_text(encoding="utf-8"))["accounts"]
    src_by_email = {}
    for a in src_accounts:
        em = (
            (a.get("extra") or {}).get("email")
            or (a.get("credentials") or {}).get("email")
            or a.get("name")
        )
        src_by_email[em] = a

    accounts = []
    for page in range(1, 30):
        st, raw = req(
            "GET",
            f"/api/v1/admin/accounts?platform=grok&page_size=100&page={page}&sort_by=id&sort_order=desc",
            token=token,
        )
        items = json.loads(raw).get("data", {}).get("items") or []
        if not items:
            break
        for a in items:
            if 1860 <= a["id"] <= 1944:
                accounts.append(a)
        if min(x["id"] for x in items) < 1860:
            break
    accounts = sorted({a["id"]: a for a in accounts}.values(), key=lambda x: x["id"])
    print("accounts", len(accounts))

    results = {"fix": [], "probes": [], "summary": {}}

    for a in accounts:
        em = (a.get("extra") or {}).get("email") or a.get("name")
        src = src_by_email.get(em)
        if not src:
            results["fix"].append({"id": a["id"], "err": "no_src"})
            continue
        cred = dict(src.get("credentials") or {})
        exp_fixed = to_expires_at(cred)
        cred["expires_at"] = exp_fixed
        body = {
            "credentials": cred,
            "extra": src.get("extra") or {},
            "proxy_id": PROXY_ID,
            "status": "active",
            "error_message": "",
        }
        st, raw = req("PUT", f"/api/v1/admin/accounts/{a['id']}", body, token=token)
        row = {"id": a["id"], "email": em, "http": st, "expires_at": exp_fixed}
        if st != 200:
            row["err"] = safe(raw)
        results["fix"].append(row)
    print("fixed", sum(1 for r in results["fix"] if r.get("http") == 200))

    st, raw = req("GET", "/api/v1/admin/accounts/1860", token=token)
    acc = json.loads(raw)["data"]
    print(
        "verify status",
        acc.get("status"),
        "expires",
        (acc.get("credentials") or {}).get("expires_at"),
        "cred_status",
        acc.get("credentials_status"),
        "err",
        (acc.get("error_message") or "")[:100],
    )

    st, raw = req(
        "POST",
        f"/api/v1/admin/proxies/{PROXY_ID}/grok-reasoning-probe",
        {"account_id": 1860, "confirm_quota_cost": True},
        token=token,
        timeout=120,
    )
    print("smoke probe", st, safe(raw, 300))

    if st != 200:
        item = src_accounts[0]
        cred = dict(item["credentials"])
        cred["expires_at"] = to_expires_at(cred)
        create_body = {
            "name": f"tmp-json85-probe-{int(time.time())}",
            "platform": "grok",
            "type": "oauth",
            "credentials": cred,
            "extra": item.get("extra") or {},
            "proxy_id": PROXY_ID,
            "concurrency": 1,
            "priority": 1,
            "skip_default_group_bind": True,
        }
        st2, raw2 = req("POST", "/api/v1/admin/accounts", create_body, token=token)
        print("create", st2, safe(raw2, 250))
        if st2 in (200, 201):
            new_id = json.loads(raw2)["data"]["id"]
            st3, raw3 = req(
                "POST",
                f"/api/v1/admin/proxies/{PROXY_ID}/grok-reasoning-probe",
                {"account_id": new_id, "confirm_quota_cost": True},
                token=token,
                timeout=120,
            )
            print("new probe", new_id, st3, safe(raw3, 350))
            results["smoke_new"] = {"id": new_id, "http": st3, "raw": safe(raw3, 500)}

    status_counter = Counter()
    visible = []
    print("=== FULL PROBE ===")
    for i, a in enumerate(accounts, 1):
        aid = a["id"]
        em = (a.get("extra") or {}).get("email") or a.get("name")
        t0 = time.time()
        st, raw = req(
            "POST",
            f"/api/v1/admin/proxies/{PROXY_ID}/grok-reasoning-probe",
            {"account_id": aid, "confirm_quota_cost": True},
            token=token,
            timeout=120,
        )
        dt = round(time.time() - t0, 2)
        row = {"i": i, "account_id": aid, "email": em, "http": st, "dt": dt}
        if st == 200:
            data = json.loads(raw).get("data") or json.loads(raw)
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
                    visible.append(aid)
        else:
            row["err"] = safe(raw)
            status_counter[f"http_{st}"] += 1
        results["probes"].append(row)
        if i <= 5 or i % 10 == 0 or row.get("visible"):
            print(
                f"[{i}/85] id={aid} http={st} status={row.get('status')} vis={row.get('visible')} "
                f"chars={row.get('visible_reasoning_chars')} rtok={row.get('reasoning_tokens')} "
                f"dt={dt} msg={safe(str(row.get('message') or row.get('err') or ''), 70)}"
            )
        time.sleep(0.3)

    ok = [p for p in results["probes"] if p.get("http") == 200]
    vis = [p for p in ok if p.get("visible")]
    results["summary"] = {
        "fixed": sum(1 for r in results["fix"] if r.get("http") == 200),
        "probe_ok": len(ok),
        "visible": f"{len(vis)}/{len(ok) if ok else 0}",
        "visible_ids": visible,
        "status_counts": dict(status_counter),
    }
    OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
    print("SUMMARY", results["summary"])
    print("saved", OUT)


if __name__ == "__main__":
    main()
