#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Continue/finish US3 visible-reasoning probes for imported JSON85 accounts."""
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
OUT = Path("/tmp/json85_us3_visible_probe_final.json")
LOG = Path("/tmp/json85_us3_probe_continue.log")
PROXY_ID = 5
ID_LO, ID_HI = 1860, 1944
PROBE_TIMEOUT = 55  # service timeout is 45s


def log(msg: str) -> None:
    line = msg if msg.endswith("\n") else msg + "\n"
    print(msg, flush=True)
    with LOG.open("a", encoding="utf-8") as f:
        f.write(line)


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


def safe(s, n=180):
    return (s or "")[:n].encode("unicode_escape").decode("ascii")


def jwt_exp(tok: str):
    try:
        p = tok.split(".")[1]
        p += "=" * ((4 - len(p) % 4) % 4)
        return json.loads(base64.urlsafe_b64decode(p)).get("exp")
    except Exception:
        return None


def to_expires_at(cred: dict) -> str:
    exp = jwt_exp(cred.get("access_token") or "")
    if exp and exp > time.time():
        return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(exp))
    return str(cred.get("expires_at") or "")


def save(results):
    OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")


def main():
    LOG.write_text("", encoding="utf-8")
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

    # load accounts in range
    accounts = []
    for page in range(1, 40):
        st, raw = req(
            "GET",
            f"/api/v1/admin/accounts?platform=grok&page_size=100&page={page}&sort_by=id&sort_order=desc",
            token=token,
        )
        items = json.loads(raw).get("data", {}).get("items") or []
        if not items:
            break
        for a in items:
            if ID_LO <= a["id"] <= ID_HI:
                accounts.append(a)
        if min(x["id"] for x in items) < ID_LO:
            break
    accounts = sorted({a["id"]: a for a in accounts}.values(), key=lambda x: x["id"])
    log(f"accounts={len(accounts)}")

    results = {
        "proxy_id": PROXY_ID,
        "fix": [],
        "probes": [],
        "summary": {},
    }

    # ensure expires fixed (cheap; skip if already RFC3339-looking and active)
    for a in accounts:
        em = (a.get("extra") or {}).get("email") or a.get("name")
        src = src_by_email.get(em)
        if not src:
            continue
        cred = dict(src.get("credentials") or {})
        cred["expires_at"] = to_expires_at(cred)
        st, raw = req(
            "PUT",
            f"/api/v1/admin/accounts/{a['id']}",
            {
                "credentials": cred,
                "extra": src.get("extra") or {},
                "proxy_id": PROXY_ID,
                "status": "active",
                "error_message": "",
            },
            token=token,
            timeout=30,
        )
        results["fix"].append({"id": a["id"], "http": st, "expires_at": cred["expires_at"]})
    log(f"fixed={sum(1 for r in results['fix'] if r.get('http')==200)}")
    save(results)

    status_counter = Counter()
    visible = []
    for i, a in enumerate(accounts, 1):
        aid = a["id"]
        em = (a.get("extra") or {}).get("email") or a.get("name")
        t0 = time.time()
        st, raw = req(
            "POST",
            f"/api/v1/admin/proxies/{PROXY_ID}/grok-reasoning-probe",
            {"account_id": aid, "confirm_quota_cost": True},
            token=token,
            timeout=PROBE_TIMEOUT,
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
                    visible.append({"account_id": aid, "email": em, "chars": row.get("visible_reasoning_chars")})
        else:
            row["err"] = safe(raw)
            status_counter[f"http_{st}"] += 1
        results["probes"].append(row)
        ok = [p for p in results["probes"] if p.get("http") == 200]
        vis_n = sum(1 for p in ok if p.get("visible"))
        results["summary"] = {
            "done": i,
            "total": len(accounts),
            "probe_ok": len(ok),
            "visible": f"{vis_n}/{len(ok) if ok else 0}",
            "visible_accounts": visible,
            "status_counts": dict(status_counter),
        }
        save(results)
        log(
            f"[{i}/{len(accounts)}] id={aid} http={st} status={row.get('status')} "
            f"vis={row.get('visible')} chars={row.get('visible_reasoning_chars')} "
            f"rtok={row.get('reasoning_tokens')} dt={dt} "
            f"msg={safe(str(row.get('message') or row.get('err') or ''), 70)}"
        )
        time.sleep(0.2)

    log("SUMMARY " + json.dumps(results["summary"], ensure_ascii=False))
    log("saved " + str(OUT))


if __name__ == "__main__":
    main()
