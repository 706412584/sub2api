#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Gateway pin retest on true group members with quota."""
from __future__ import annotations

import json
import random
import time
import urllib.error
import urllib.request
from pathlib import Path

BASE = "http://127.0.0.1:18080"
OUT = Path("/tmp/random_quota_gateway_pin_fixup.json")
PROMPT = "Solve step by step: what is 17*19? Show brief reasoning."
MODEL = "grok-4.5"
RNG = random.Random(42)


def req(method, path, data=None, token=None, apikey=None, timeout=150, stream=False):
    h = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        h["Authorization"] = f"Bearer {token}"
    if apikey:
        h["Authorization"] = f"Bearer {apikey}"
        if stream:
            h["Accept"] = "text/event-stream"
    body = None if data is None else json.dumps(data).encode()
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


def rem_req(a):
    extra = a.get("extra") or {}
    snap = extra.get("grok_usage_snapshot") or {}
    reqs = snap.get("requests") or {}
    rem = reqs.get("remaining")
    if rem is None:
        try:
            rem = int((snap.get("headers") or {}).get("x-ratelimit-remaining-requests"))
        except Exception:
            rem = None
    return rem


def list_group(token, gid, max_pages=12):
    items = []
    for page in range(1, max_pages + 1):
        st, raw = req(
            "GET",
            f"/api/v1/admin/accounts?group_id={gid}&page_size=100&page={page}&status=active",
            token=token,
        )
        if st != 200:
            break
        batch = json.loads(raw).get("data", {}).get("items") or []
        if not batch:
            break
        items.extend(batch)
        if len(batch) < 100:
            break
    return items


def set_sched(token, aid, v):
    return req(
        "POST",
        f"/api/v1/admin/accounts/{aid}/schedulable",
        {"schedulable": v},
        token=token,
    )


def parse_chat(raw):
    j = json.loads(raw)
    msg = ((j.get("choices") or [{}])[0] or {}).get("message") or {}
    rc = msg.get("reasoning_content") or ""
    if not isinstance(rc, str):
        rc = str(rc or "")
    usage = j.get("usage") or {}
    rtok = (
        usage.get("reasoning_tokens")
        or (usage.get("completion_tokens_details") or {}).get("reasoning_tokens")
        or 0
    )
    content = msg.get("content") or ""
    if not isinstance(content, str):
        content = str(content or "")
    return {
        "visible": len(rc) > 0,
        "reasoning_chars": len(rc),
        "preview": safe(rc, 80),
        "rtok": rtok,
        "content_chars": len(content),
    }


def parse_msg(raw):
    j = json.loads(raw)
    tchars = 0
    types = []
    preview = ""
    for c in j.get("content") or []:
        if not isinstance(c, dict):
            continue
        types.append(c.get("type"))
        if c.get("type") == "thinking":
            th = c.get("thinking") or ""
            tchars += len(th)
            if not preview and th:
                preview = th
    return {
        "visible": tchars > 0,
        "thinking_chars": tchars,
        "types": types,
        "preview": safe(preview, 80),
        "usage": j.get("usage"),
    }


def pick(token, gid, n=4):
    items = list_group(token, gid)
    cands = []
    for a in items:
        gids = set(a.get("group_ids") or [])
        if gid not in gids:
            continue
        if a.get("schedulable") is False:
            continue
        if a.get("platform") != "grok" or a.get("type") != "oauth":
            continue
        rem = rem_req(a)
        if rem is None or int(rem) <= 0:
            continue
        cands.append(a)
    RNG.shuffle(cands)
    sample = cands[:n]
    print(
        f"group{gid}: listed={len(items)} strict_quota={len(cands)} sample={[a['id'] for a in sample]}"
    )
    return sample


def pin_and_call(token, key, gid, target, items, bucket, results, disabled):
    disabled_this = []
    for a in items:
        aid = a["id"]
        if aid == target:
            continue
        if gid not in set(a.get("group_ids") or []):
            continue
        if a.get("schedulable") is False:
            continue
        st, _ = set_sched(token, aid, False)
        if st == 200:
            disabled_this.append(aid)
            disabled.append(aid)
    set_sched(token, target, True)
    time.sleep(1.0)
    print(f"G{gid} pin {target} disabled {len(disabled_this)}")

    t0 = time.time()
    st, raw = req(
        "POST",
        "/v1/chat/completions",
        data={
            "model": MODEL,
            "messages": [{"role": "user", "content": PROMPT}],
            "stream": False,
        },
        apikey=key,
        timeout=120,
    )
    row = {
        "group": gid,
        "account_id": target,
        "path": "chat",
        "http": st,
        "dt": round(time.time() - t0, 2),
    }
    if st == 200:
        row.update(parse_chat(raw))
    else:
        row["err"] = safe(raw)
    results[bucket].append(row)
    print(" ", row)

    t0 = time.time()
    st, raw = req(
        "POST",
        "/v1/messages",
        data={
            "model": MODEL,
            "max_tokens": 512,
            "messages": [{"role": "user", "content": PROMPT}],
            "stream": False,
            "thinking": {"type": "enabled", "budget_tokens": 1024},
        },
        apikey=key,
        timeout=150,
    )
    row = {
        "group": gid,
        "account_id": target,
        "path": "messages",
        "http": st,
        "dt": round(time.time() - t0, 2),
    }
    if st == 200:
        row.update(parse_msg(raw))
    else:
        row["err"] = safe(raw)
    results[bucket].append(row)
    print(" ", row)

    for aid in disabled_this:
        set_sched(token, aid, True)
    return disabled_this


def main():
    results = {"g2": [], "g5": [], "usage": [], "restore": []}
    disabled = []

    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    token = json.loads(raw)["data"]["access_token"]
    st, raw = req("GET", "/api/v1/admin/groups/2/api-keys", token=token)
    key2 = json.loads(raw)["data"]["items"][0]["key"]
    st, raw = req("GET", "/api/v1/admin/groups/5/api-keys", token=token)
    key5 = json.loads(raw)["data"]["items"][0]["key"]

    try:
        g2 = pick(token, 2, n=4)
        items2 = list_group(token, 2)
        for acc in g2[:3]:
            target = acc["id"]
            restored = pin_and_call(
                token, key2, 2, target, items2, "g2", results, disabled
            )
            disabled = [x for x in disabled if x not in restored]
            time.sleep(0.6)

        prefer = [749, 819, 835, 823, 741]
        g5_items = list_group(token, 5)
        byid = {a["id"]: a for a in g5_items}
        g5_targets = []
        for aid in prefer:
            a = byid.get(aid)
            if not a:
                continue
            if 5 not in set(a.get("group_ids") or []):
                continue
            if a.get("schedulable") is False:
                continue
            g5_targets.append(aid)
            if len(g5_targets) >= 3:
                break
        if len(g5_targets) < 3:
            extra = pick(token, 5, n=5)
            for a in extra:
                if a["id"] not in g5_targets:
                    g5_targets.append(a["id"])
                if len(g5_targets) >= 3:
                    break
        print("G5 targets", g5_targets)

        items5 = list_group(token, 5)
        for target in g5_targets[:3]:
            restored = pin_and_call(
                token, key5, 5, target, items5, "g5", results, disabled
            )
            disabled = [x for x in disabled if x not in restored]
            time.sleep(0.6)

        st, raw = req("GET", "/api/v1/admin/usage?page_size=20", token=token)
        if st == 200:
            for u in json.loads(raw)["data"]["items"]:
                results["usage"].append(
                    {
                        k: u.get(k)
                        for k in [
                            "id",
                            "account_id",
                            "group_id",
                            "inbound_endpoint",
                            "upstream_endpoint",
                            "reasoning_tokens",
                            "stream",
                            "model",
                        ]
                    }
                )
    finally:
        print("RESTORE leftover", len(set(disabled)))
        for aid in sorted(set(disabled)):
            set_sched(token, aid, True)
            results["restore"].append(aid)

    def rate(rows, path=None):
        rs = [r for r in rows if path is None or r.get("path") == path]
        ok = [r for r in rs if r.get("http") == 200]
        if not ok:
            return "0/0"
        return f"{sum(1 for r in ok if r.get('visible'))}/{len(ok)}"

    summary = {
        "g2_chat": rate(results["g2"], "chat"),
        "g2_messages": rate(results["g2"], "messages"),
        "g5_chat": rate(results["g5"], "chat"),
        "g5_messages": rate(results["g5"], "messages"),
        "g2_accounts": sorted(
            {r["account_id"] for r in results["g2"] if r.get("http") == 200}
        ),
        "g5_accounts": sorted(
            {r["account_id"] for r in results["g5"] if r.get("http") == 200}
        ),
    }
    results["summary"] = summary
    OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
    print("SUMMARY", summary)
    for u in results["usage"][:12]:
        print("usage", u)


if __name__ == "__main__":
    main()
