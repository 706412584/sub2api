#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Follow-up: pin OpenAI-success accounts into group5 and retest /v1/messages chat protocol."""
from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from pathlib import Path

BASE = "http://127.0.0.1:18080"
OUT = Path("/tmp/pin_success_anthropic_messages_g5_fixup.json")
MODEL = "grok-4.5"
PROMPT = "Solve step by step: what is 17*19? Show brief reasoning."
KEEP = [1744, 1745, 772]


def req(method, path, data=None, token=None, apikey=None, timeout=180, stream=False):
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if apikey:
        headers["Authorization"] = f"Bearer {apikey}"
        if stream:
            headers["Accept"] = "text/event-stream"
    body = None if data is None else json.dumps(data, ensure_ascii=False).encode("utf-8")
    r = urllib.request.Request(BASE + path, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, str(e)


def safe(s, n=300):
    return (s or "")[:n].encode("unicode_escape").decode("ascii")


def get_acc(token, aid):
    st, raw = req("GET", f"/api/v1/admin/accounts/{aid}", token=token)
    return (json.loads(raw)["data"] if st == 200 else None), st, raw


def set_sched(token, aid, v):
    return req(
        "POST",
        f"/api/v1/admin/accounts/{aid}/schedulable",
        {"schedulable": v},
        token=token,
    )


def set_proxy(token, aid, pid):
    return req("PUT", f"/api/v1/admin/accounts/{aid}", {"proxy_id": pid}, token=token)


def set_groups(token, aid, gids):
    return req("PUT", f"/api/v1/admin/accounts/{aid}", {"group_ids": gids}, token=token)


def list_group(token, gid, max_pages=10):
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


def parse_ns(raw):
    j = json.loads(raw)
    thinking_chars = 0
    text_chars = 0
    types = []
    preview = ""
    blocks = []
    for c in j.get("content") or []:
        if not isinstance(c, dict):
            continue
        t = c.get("type")
        types.append(t)
        th = c.get("thinking") or ""
        tx = c.get("text") or ""
        sig = c.get("signature")
        if t == "thinking":
            thinking_chars += len(th)
            if not preview and th:
                preview = th
            blocks.append(
                {
                    "type": t,
                    "thinking_len": len(th),
                    "sig_len": len(sig or ""),
                    "keys": sorted(c.keys()),
                }
            )
        if t == "text":
            text_chars += len(tx)
            blocks.append({"type": t, "text_len": len(tx)})
    usage = j.get("usage") or {}
    return {
        "visible": thinking_chars > 0,
        "thinking_chars": thinking_chars,
        "text_chars": text_chars,
        "content_types": types,
        "blocks": blocks,
        "thinking_preview": safe(preview, 120),
        "usage": usage,
    }


def parse_s(raw):
    thinking_deltas = 0
    thinking_chars = 0
    text_deltas = 0
    event_types = {}
    starts = []
    for line in raw.splitlines():
        if not line.startswith("data:"):
            continue
        data = line[5:].strip()
        if not data or data == "[DONE]":
            continue
        try:
            obj = json.loads(data)
        except Exception:
            continue
        et = obj.get("type") or "unknown"
        event_types[et] = event_types.get(et, 0) + 1
        if et == "content_block_start":
            cb = obj.get("content_block") or {}
            starts.append(
                {
                    "type": cb.get("type"),
                    "keys": sorted(cb.keys()),
                    "thinking_len": len(cb.get("thinking") or ""),
                }
            )
        if et == "content_block_delta":
            delta = obj.get("delta") or {}
            dt = delta.get("type")
            if dt == "thinking_delta":
                thinking_deltas += 1
                thinking_chars += len(delta.get("thinking") or "")
            if dt == "text_delta":
                text_deltas += 1
    return {
        "visible": thinking_chars > 0,
        "thinking_chars": thinking_chars,
        "thinking_deltas": thinking_deltas,
        "text_deltas": text_deltas,
        "event_top": dict(sorted(event_types.items(), key=lambda x: -x[1])[:12]),
        "block_starts": starts,
        "raw_thinking_delta": raw.count("thinking_delta"),
    }


def main():
    results = {"setup": [], "calls": [], "usage": [], "restore": []}
    disabled = []
    group_restore = []
    proxy_restore = []

    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    token = json.loads(raw)["data"]["access_token"]
    st, raw = req("GET", "/api/v1/admin/groups/5/api-keys", token=token)
    key5 = json.loads(raw)["data"]["items"][0]["key"]

    try:
        for aid in KEEP:
            acc, st, raw = get_acc(token, aid)
            if not acc:
                results["setup"].append({"id": aid, "err": safe(raw)})
                continue
            orig_groups = list(acc.get("group_ids") or [])
            orig_proxy = acc.get("proxy_id")
            group_restore.append((aid, orig_groups))
            proxy_restore.append((aid, orig_proxy))
            row = {
                "id": aid,
                "orig_groups": orig_groups,
                "orig_proxy": orig_proxy,
                "orig_sched": acc.get("schedulable"),
            }
            new_groups = sorted(set(orig_groups) | {5})
            if set(new_groups) != set(orig_groups):
                st2, raw2 = set_groups(token, aid, new_groups)
                row["set_groups"] = st2
                if st2 != 200:
                    row["set_groups_err"] = safe(raw2)
            else:
                row["set_groups"] = "already"
            if orig_proxy != 5:
                st3, raw3 = set_proxy(token, aid, 5)
                row["set_proxy5"] = st3
                if st3 != 200:
                    row["set_proxy_err"] = safe(raw3)
            else:
                row["set_proxy5"] = "already"
            if acc.get("schedulable") is False:
                set_sched(token, aid, True)
                row["force_sched"] = True
            acc2, _, _ = get_acc(token, aid)
            row["after_groups"] = acc2.get("group_ids") if acc2 else None
            results["setup"].append(row)
            print("setup", row)

        keep = set(KEEP)
        items = list_group(token, 5)
        for a in items:
            aid = a.get("id")
            if aid in keep:
                continue
            if a.get("schedulable") is False:
                continue
            st, raw = set_sched(token, aid, False)
            if st == 200:
                disabled.append(aid)
        print(
            f"disabled {len(disabled)} others in g5; listed={len(items)} keep={sorted(keep)}"
        )
        results["setup"].append(
            {"disabled": len(disabled), "listed": len(items), "keep": sorted(keep)}
        )
        time.sleep(1.5)

        for i, stream in [
            (1, False),
            (2, False),
            (3, False),
            (4, True),
            (5, True),
            (6, True),
        ]:
            payload = {
                "model": MODEL,
                "max_tokens": 512,
                "messages": [{"role": "user", "content": PROMPT}],
                "stream": stream,
                "thinking": {"type": "enabled", "budget_tokens": 1024},
            }
            t0 = time.time()
            st, raw = req(
                "POST",
                "/v1/messages",
                data=payload,
                apikey=key5,
                timeout=150,
                stream=stream,
            )
            dt = round(time.time() - t0, 2)
            row = {"i": i, "stream": stream, "http": st, "dt": dt, "bytes": len(raw)}
            if st == 200:
                row.update(parse_s(raw) if stream else parse_ns(raw))
                if i in (1, 4):
                    ext = "txt" if stream else "json"
                    Path(f"/tmp/g5_messages_sample_{i}.{ext}").write_text(
                        raw, encoding="utf-8"
                    )
            else:
                row["err"] = safe(raw)
            results["calls"].append(row)
            print(
                f"G5_{'s' if stream else 'ns'}_{i} http={st} vis={row.get('visible')} "
                f"tchars={row.get('thinking_chars')} types={row.get('content_types') or row.get('block_starts')} "
                f"events={row.get('event_top')} dt={dt} err={row.get('err')}"
            )
            time.sleep(1.2)

        st, raw = req("GET", "/api/v1/admin/usage?page_size=12", token=token)
        if st == 200:
            for u in json.loads(raw).get("data", {}).get("items") or []:
                if u.get("group_id") == 5 or u.get("account_id") in keep:
                    results["usage"].append(
                        {
                            "id": u.get("id"),
                            "account_id": u.get("account_id"),
                            "group_id": u.get("group_id"),
                            "inbound": u.get("inbound_endpoint"),
                            "upstream": u.get("upstream_endpoint"),
                            "stream": u.get("stream"),
                            "reasoning_tokens": u.get("reasoning_tokens"),
                            "model": u.get("model"),
                        }
                    )
        print("usage", results["usage"][:8])

    finally:
        print("RESTORE")
        for aid in disabled:
            st, raw = set_sched(token, aid, True)
            results["restore"].append({"reenable": aid, "http": st})
        print("reenabled", len(disabled))
        for aid, orig in group_restore:
            acc, _, _ = get_acc(token, aid)
            cur = list(acc.get("group_ids") or []) if acc else None
            if cur is not None and sorted(cur) != sorted(orig):
                st, raw = set_groups(token, aid, orig)
                results["restore"].append(
                    {"groups": aid, "from": cur, "to": orig, "http": st}
                )
                print("restore groups", aid, cur, "->", orig, st)
        for aid, orig in proxy_restore:
            if orig is None:
                continue
            acc, _, _ = get_acc(token, aid)
            cur = acc.get("proxy_id") if acc else None
            if cur != orig:
                st, raw = set_proxy(token, aid, orig)
                results["restore"].append(
                    {"proxy": aid, "from": cur, "to": orig, "http": st}
                )
                print("restore proxy", aid, cur, "->", orig, st)

    ok = [r for r in results["calls"] if r.get("http") == 200]
    vis = sum(1 for r in ok if r.get("visible"))
    results["summary"] = {
        "g5_visible": f"{vis}/{len(ok)}",
        "accounts_used": sorted(
            {u.get("account_id") for u in results["usage"] if u.get("account_id")}
        ),
    }
    OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
    print("SUMMARY", results["summary"])
    print("saved", OUT)


if __name__ == "__main__":
    main()
