#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""US3 Chat Completions retest for JSON85 accounts (1860-1944).

Previous probe used Responses only. This hits gateway /v1/chat/completions
via an exclusive temp group bound to proxy 5, pinning one account at a time.
Restores group membership / key / schedulable in finally.
"""
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
OUT = Path("/tmp/json85_us3_chat_completions_probe.json")
LOG = Path("/tmp/json85_us3_chat_completions_probe.log")
PROXY_ID = 5
ID_LO, ID_HI = 1860, 1944
MODEL = "grok-4.5"
PROMPT = "Solve step by step: what is 17*19? Show brief reasoning."
GROUP_ID = 12  # tmp exclusive group created earlier; recreate if missing
KEY_ID = 2  # temporarily rebind group2 key
ORIG_KEY_GROUP = 2
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


def save(results):
    OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")


def parse_chat(raw: str) -> dict:
    try:
        j = json.loads(raw)
    except Exception:
        return {"visible": False, "err": "bad_json", "raw": safe(raw, 120)}
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
        "finish_reason": ((j.get("choices") or [{}])[0] or {}).get("finish_reason"),
    }


def set_sched(token, aid, v):
    return req(
        "POST",
        f"/api/v1/admin/accounts/{aid}/schedulable",
        {"schedulable": v},
        token=token,
        timeout=20,
    )


def main():
    LOG.write_text("", encoding="utf-8")
    results = {
        "proxy_id": PROXY_ID,
        "path": "/v1/chat/completions",
        "model": MODEL,
        "group_id": GROUP_ID,
        "fix": [],
        "setup": {},
        "probes": [],
        "summary": {},
        "restore": [],
    }

    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    if st != 200:
        raise SystemExit(f"login failed {st} {safe(raw)}")
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

    # ensure temp group
    st, raw = req("GET", f"/api/v1/admin/groups/{GROUP_ID}", token=token)
    if st != 200:
        st, raw = req(
            "POST",
            "/api/v1/admin/groups",
            {
                "name": f"tmp-json85-chat-{int(time.time())}",
                "platform": "grok",
                "is_exclusive": True,
                "default_proxy_id": PROXY_ID,
                "subscription_type": "standard",
                "rate_multiplier": 1,
                "grok_messages_protocol": "chat_completions",
            },
            token=token,
        )
        if st not in (200, 201):
            raise SystemExit(f"create group failed {st} {safe(raw)}")
        GROUP = json.loads(raw)["data"]["id"]
        results["group_id"] = GROUP
    else:
        GROUP = GROUP_ID
        # ensure exclusive + default proxy
        req(
            "PUT",
            f"/api/v1/admin/groups/{GROUP}",
            {
                "is_exclusive": True,
                "default_proxy_id": PROXY_ID,
                "status": "active",
            },
            token=token,
        )
    results["group_id"] = GROUP
    log(f"group={GROUP}")

    # rebind key 2 -> temp group
    st, raw = req(
        "PUT", f"/api/v1/admin/api-keys/{KEY_ID}", {"group_id": GROUP}, token=token
    )
    results["setup"]["rebind_key"] = {"http": st, "raw": safe(raw, 120)}
    if st != 200:
        raise SystemExit(f"rebind key failed {st} {safe(raw)}")
    st, raw = req("GET", f"/api/v1/admin/groups/{GROUP}/api-keys", token=token)
    items = (json.loads(raw).get("data") or {}).get("items") or []
    if not items:
        # fallback read key from original listing
        st, raw = req("GET", f"/api/v1/admin/groups/{ORIG_KEY_GROUP}/api-keys", token=token)
        # key moved so empty; get key value before move already lost — fetch by id via user
        raise SystemExit("no api key on temp group after rebind")
    apikey = items[0]["key"]
    results["setup"]["api_key_id"] = items[0]["id"]
    log(f"apikey id={items[0]['id']}")

    # load accounts
    accounts = []
    for page in range(1, 40):
        st, raw = req(
            "GET",
            f"/api/v1/admin/accounts?platform=grok&page_size=100&page={page}&sort_by=id&sort_order=desc",
            token=token,
        )
        batch = json.loads(raw).get("data", {}).get("items") or []
        if not batch:
            break
        for a in batch:
            if ID_LO <= a["id"] <= ID_HI:
                accounts.append(a)
        if min(x["id"] for x in batch) < ID_LO:
            break
    accounts = sorted({a["id"]: a for a in accounts}.values(), key=lambda x: x["id"])
    log(f"accounts={len(accounts)}")
    if len(accounts) != 85:
        log(f"WARN expected 85 got {len(accounts)}")

    # fix credentials + bind to temp group + proxy 5 + schedulable false initially
    for a in accounts:
        em = (a.get("extra") or {}).get("email") or a.get("name")
        src = src_by_email.get(em)
        if not src:
            results["fix"].append({"id": a["id"], "err": "no_src"})
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
                "group_ids": [GROUP],
                "status": "active",
                "error_message": "",
                "schedulable": False,
            },
            token=token,
            timeout=30,
        )
        results["fix"].append(
            {
                "id": a["id"],
                "http": st,
                "expires_at": cred["expires_at"],
                "jwt_left": int((jwt_exp(cred.get("access_token") or "") or 0) - time.time()),
            }
        )
        if st != 200:
            results["fix"][-1]["err"] = safe(raw)
    fixed_ok = sum(1 for r in results["fix"] if r.get("http") == 200)
    log(f"fixed_bound={fixed_ok}/{len(accounts)}")
    save(results)

    status_counter = Counter()
    visible = []
    disabled_others = []

    try:
        for i, a in enumerate(accounts, 1):
            aid = a["id"]
            em = (a.get("extra") or {}).get("email") or a.get("name")

            # pin: enable only this account among the 85
            # disable previously enabled if any
            for prev in list(disabled_others):
                set_sched(token, prev, False)
            disabled_others.clear()

            st_en, _ = set_sched(token, aid, True)
            # ensure others stay false — only toggle target for speed (group exclusive + only these members)
            # But other 84 may still be schedulable if we enabled mid-run; force disable all except target every 10th or always cheap?
            # Initial all False; only enable target each round and disable previous target.
            if i > 1:
                prev_id = accounts[i - 2]["id"]
                set_sched(token, prev_id, False)
            time.sleep(0.15)

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
                "enable_http": st_en,
            }
            if st == 200:
                parsed = parse_chat(raw)
                row.update(parsed)
                status_counter["visible" if parsed.get("visible") else "no_visible"] += 1
                if parsed.get("visible"):
                    visible.append(
                        {
                            "account_id": aid,
                            "email": em,
                            "chars": parsed.get("reasoning_chars"),
                            "rtok": parsed.get("reasoning_tokens"),
                        }
                    )
            else:
                row["err"] = safe(raw)
                status_counter[f"http_{st}"] += 1

            # disable target after call (keep pool clean)
            set_sched(token, aid, False)

            results["probes"].append(row)
            ok = [p for p in results["probes"] if p.get("http") == 200]
            vis_n = sum(1 for p in ok if p.get("visible"))
            results["summary"] = {
                "done": i,
                "total": len(accounts),
                "chat_ok": len(ok),
                "visible": f"{vis_n}/{len(ok) if ok else 0}",
                "visible_accounts": visible,
                "status_counts": dict(status_counter),
            }
            save(results)
            log(
                f"[{i}/{len(accounts)}] id={aid} http={st} vis={row.get('visible')} "
                f"rchars={row.get('reasoning_chars')} rtok={row.get('reasoning_tokens')} "
                f"dt={dt} msg={safe(str(row.get('err') or row.get('reasoning_preview') or ''), 70)}"
            )
            time.sleep(0.15)
    finally:
        log("RESTORE")
        # unbind accounts from temp group, schedulable false, keep proxy 5
        for a in accounts:
            aid = a["id"]
            st, raw = req(
                "PUT",
                f"/api/v1/admin/accounts/{aid}",
                {"group_ids": [], "schedulable": False},
                token=token,
                timeout=20,
            )
            results["restore"].append({"account_id": aid, "unbind_http": st})
            set_sched(token, aid, False)
        # rebind key back to group 2
        st, raw = req(
            "PUT",
            f"/api/v1/admin/api-keys/{KEY_ID}",
            {"group_id": ORIG_KEY_GROUP},
            token=token,
        )
        results["restore"].append({"rebind_key": KEY_ID, "group": ORIG_KEY_GROUP, "http": st})
        log(f"key restore http={st}")
        save(results)

    log("SUMMARY " + json.dumps(results["summary"], ensure_ascii=False))
    log("saved " + str(OUT))


if __name__ == "__main__":
    main()
