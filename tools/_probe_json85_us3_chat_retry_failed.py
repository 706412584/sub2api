#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Retry failed JSON85 Chat Completions probes one-account-at-a-time on US3."""
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
PREV = Path("/tmp/json85_us3_chat_completions_probe.json")
OUT = Path("/tmp/json85_us3_chat_completions_retry.json")
LOG = Path("/tmp/json85_us3_chat_completions_retry.log")
PROXY_ID = 5
GROUP_ID = 12
KEY_ID = 2
ORIG_KEY_GROUP = 2
MODEL = "grok-4.5"
PROMPT = "Solve step by step: what is 17*19? Show brief reasoning."
CHAT_TIMEOUT = 90


def log(msg: str) -> None:
    line = msg if msg.endswith("\n") else msg + "\n"
    print(msg, flush=True)
    with LOG.open("a", encoding="utf-8") as f:
        f.write(line)


def req(method, path, data=None, token=None, apikey=None, timeout=120):
    h = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        h["Authorization"] = f"Bearer {token}"
    if apikey:
        h["Authorization"] = f"Bearer {apikey}"
    body = None if data is None else json.dumps(data, ensure_ascii=False).encode()
    r = urllib.request.Request(BASE + path, data=body, headers=h, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, str(e)


def safe(s, n=160):
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


def parse_chat(raw: str) -> dict:
    try:
        j = json.loads(raw)
    except Exception:
        return {"visible": False, "err": "bad_json"}
    msg = ((j.get("choices") or [{}])[0] or {}).get("message") or {}
    rc = msg.get("reasoning_content") or msg.get("reasoning") or ""
    if not isinstance(rc, str):
        rc = str(rc or "")
    content = msg.get("content") or ""
    if not isinstance(content, str):
        content = str(content or "")
    usage = j.get("usage") or {}
    rtok = int(
        usage.get("reasoning_tokens")
        or (usage.get("completion_tokens_details") or {}).get("reasoning_tokens")
        or 0
    )
    return {
        "visible": len(rc) > 0,
        "reasoning_chars": len(rc),
        "reasoning_preview": safe(rc, 80),
        "content_chars": len(content),
        "reasoning_tokens": rtok,
    }


def save(results):
    OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")


def main():
    LOG.write_text("", encoding="utf-8")
    prev = json.loads(PREV.read_text(encoding="utf-8"))
    failed_ids = sorted(
        {
            p["account_id"]
            for p in prev.get("probes") or []
            if p.get("http") != 200
        }
    )
    # also re-check first 3 successes for stability (optional small sample)
    ok_ids = [p["account_id"] for p in prev.get("probes") or [] if p.get("http") == 200][:3]
    targets = failed_ids  # focus on failed; successes already 0 visible with rtok
    log(f"retry targets={len(targets)} sample_ok={ok_ids}")

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

    # ensure group
    st, raw = req("GET", f"/api/v1/admin/groups/{GROUP_ID}", token=token)
    if st != 200:
        raise SystemExit(f"group {GROUP_ID} missing")
    req(
        "PUT",
        f"/api/v1/admin/groups/{GROUP_ID}",
        {"is_exclusive": True, "default_proxy_id": PROXY_ID, "status": "active"},
        token=token,
    )

    # rebind key
    st, raw = req(
        "PUT", f"/api/v1/admin/api-keys/{KEY_ID}", {"group_id": GROUP_ID}, token=token
    )
    if st != 200:
        raise SystemExit(f"rebind key {st} {safe(raw)}")
    st, raw = req("GET", f"/api/v1/admin/groups/{GROUP_ID}/api-keys", token=token)
    apikey = json.loads(raw)["data"]["items"][0]["key"]
    log("key rebound")

    results = {
        "proxy_id": PROXY_ID,
        "path": "/v1/chat/completions",
        "group_id": GROUP_ID,
        "retry_of": str(PREV),
        "probes": [],
        "summary": {},
        "restore": [],
    }
    # merge previous successes into final summary later
    results["prev_ok"] = [
        p for p in prev.get("probes") or [] if p.get("http") == 200
    ]

    status_counter = Counter()
    visible = []

    try:
        for i, aid in enumerate(targets, 1):
            st, raw = req("GET", f"/api/v1/admin/accounts/{aid}", token=token)
            if st != 200:
                row = {"i": i, "account_id": aid, "http": 0, "err": "missing"}
                results["probes"].append(row)
                status_counter["missing"] += 1
                continue
            acc = json.loads(raw)["data"]
            em = (acc.get("extra") or {}).get("email") or acc.get("name")
            src = src_by_email.get(em)
            if not src:
                row = {"i": i, "account_id": aid, "http": 0, "err": "no_src"}
                results["probes"].append(row)
                status_counter["no_src"] += 1
                continue

            cred = dict(src.get("credentials") or {})
            exp = jwt_exp(cred.get("access_token") or "")
            left = int(exp - time.time()) if exp else -1
            if left < 60:
                row = {
                    "i": i,
                    "account_id": aid,
                    "email": em,
                    "http": 0,
                    "err": f"jwt_expired_or_near left={left}",
                }
                results["probes"].append(row)
                status_counter["jwt_expired"] += 1
                log(f"[{i}/{len(targets)}] id={aid} SKIP jwt left={left}")
                save(results)
                continue

            cred["expires_at"] = to_expires_at(cred)
            # sole member of group, schedulable true
            st, raw = req(
                "PUT",
                f"/api/v1/admin/accounts/{aid}",
                {
                    "credentials": cred,
                    "extra": src.get("extra") or {},
                    "proxy_id": PROXY_ID,
                    "group_ids": [GROUP_ID],
                    "status": "active",
                    "error_message": "",
                    "schedulable": True,
                },
                token=token,
                timeout=30,
            )
            if st != 200:
                row = {
                    "i": i,
                    "account_id": aid,
                    "email": em,
                    "http": 0,
                    "err": f"put_failed {safe(raw)}",
                }
                results["probes"].append(row)
                status_counter["put_failed"] += 1
                log(f"[{i}/{len(targets)}] id={aid} PUT fail {safe(raw,60)}")
                save(results)
                continue

            # extra enable
            req(
                "POST",
                f"/api/v1/admin/accounts/{aid}/schedulable",
                {"schedulable": True},
                token=token,
            )
            time.sleep(0.6)

            t0 = time.time()
            st, raw = req(
                "POST",
                "/v1/chat/completions",
                data={
                    "model": MODEL,
                    "messages": [{"role": "user", "content": PROMPT}],
                    "stream": False,
                },
                apikey=apikey,
                timeout=CHAT_TIMEOUT,
            )
            dt = round(time.time() - t0, 2)
            row = {
                "i": i,
                "account_id": aid,
                "email": em,
                "http": st,
                "dt": dt,
                "jwt_left": left,
            }
            if st == 200:
                parsed = parse_chat(raw)
                row.update(parsed)
                status_counter["visible" if parsed.get("visible") else "no_visible"] += 1
                if parsed.get("visible"):
                    visible.append(
                        {
                            "account_id": aid,
                            "chars": parsed.get("reasoning_chars"),
                            "rtok": parsed.get("reasoning_tokens"),
                        }
                    )
            else:
                row["err"] = safe(raw)
                status_counter[f"http_{st}"] += 1

            # unbind immediately
            req(
                "PUT",
                f"/api/v1/admin/accounts/{aid}",
                {"group_ids": [], "schedulable": False},
                token=token,
                timeout=20,
            )
            req(
                "POST",
                f"/api/v1/admin/accounts/{aid}/schedulable",
                {"schedulable": False},
                token=token,
            )

            results["probes"].append(row)
            ok = [p for p in results["probes"] if p.get("http") == 200]
            vis_n = sum(1 for p in ok if p.get("visible"))
            results["summary"] = {
                "done": i,
                "total": len(targets),
                "chat_ok": len(ok),
                "visible": f"{vis_n}/{len(ok) if ok else 0}",
                "visible_accounts": visible,
                "status_counts": dict(status_counter),
            }
            save(results)
            log(
                f"[{i}/{len(targets)}] id={aid} http={st} vis={row.get('visible')} "
                f"rchars={row.get('reasoning_chars')} rtok={row.get('reasoning_tokens')} "
                f"dt={dt} jwt_left={left} msg={safe(str(row.get('err') or row.get('reasoning_preview') or ''), 70)}"
            )
            time.sleep(0.2)
    finally:
        # restore key
        st, raw = req(
            "PUT",
            f"/api/v1/admin/api-keys/{KEY_ID}",
            {"group_id": ORIG_KEY_GROUP},
            token=token,
        )
        results["restore"].append({"key": KEY_ID, "group": ORIG_KEY_GROUP, "http": st})
        # ensure no leftovers in group 12
        for page in range(1, 5):
            st, raw = req(
                "GET",
                f"/api/v1/admin/accounts?group_id={GROUP_ID}&page_size=100&page={page}",
                token=token,
            )
            if st != 200:
                break
            items = json.loads(raw).get("data", {}).get("items") or []
            if not items:
                break
            for a in items:
                req(
                    "PUT",
                    f"/api/v1/admin/accounts/{a['id']}",
                    {"group_ids": [], "schedulable": False},
                    token=token,
                )
        save(results)
        log(f"key restore http={st}")

    # combined summary
    combined_ok = list(results["prev_ok"]) + [
        p for p in results["probes"] if p.get("http") == 200
    ]
    # dedupe by account_id prefer retry
    by_id = {}
    for p in prev.get("probes") or []:
        by_id[p["account_id"]] = p
    for p in results["probes"]:
        by_id[p["account_id"]] = p
    all_rows = list(by_id.values())
    ok_all = [p for p in all_rows if p.get("http") == 200]
    vis_all = [p for p in ok_all if p.get("visible")]
    results["combined"] = {
        "accounts": len(all_rows),
        "chat_ok": len(ok_all),
        "visible": f"{len(vis_all)}/{len(ok_all) if ok_all else 0}",
        "visible_ids": [p["account_id"] for p in vis_all],
        "rtok_positive": sum(1 for p in ok_all if (p.get("reasoning_tokens") or 0) > 0),
        "http_fail": sum(1 for p in all_rows if p.get("http") != 200),
    }
    save(results)
    log("COMBINED " + json.dumps(results["combined"], ensure_ascii=False))
    log("SUMMARY " + json.dumps(results["summary"], ensure_ascii=False))
    log("saved " + str(OUT))


if __name__ == "__main__":
    main()
