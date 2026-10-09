#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Pin OpenAI-visible-success accounts and retest Anthropic /v1/messages."""
from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from pathlib import Path

BASE = "http://127.0.0.1:18080"
OUT = Path("/tmp/pin_success_anthropic_messages.json")
MODEL = "grok-4.5"
PROMPT = "Solve step by step: what is 17*19? Show brief reasoning."
# OpenAI-path visible successes from multi_visible_reasoning usage
PIN_IDS = [1744, 1745]
GROUP2_PIN = [1744, 1745]  # native messages -> responses
# 772 was historically interesting but group5-only; optional if in group5
GROUP5_EXTRA = [772]


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


def safe(s, n=220):
    return (s or "")[:n].encode("unicode_escape").decode("ascii")


def get_account(token, aid):
    st, raw = req("GET", f"/api/v1/admin/accounts/{aid}", token=token)
    if st != 200:
        return None, st, raw
    return json.loads(raw)["data"], st, raw


def set_proxy(token, aid, proxy_id):
    return req("PUT", f"/api/v1/admin/accounts/{aid}", {"proxy_id": proxy_id}, token=token)


def set_schedulable(token, aid, schedulable: bool):
    return req(
        "POST",
        f"/api/v1/admin/accounts/{aid}/schedulable",
        {"schedulable": schedulable},
        token=token,
    )


def list_group_accounts(token, group_id, max_pages=8):
    items = []
    for page in range(1, max_pages + 1):
        st, raw = req(
            "GET",
            f"/api/v1/admin/accounts?group_id={group_id}&page_size=100&page={page}&status=active",
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


def parse_messages_json(raw: str) -> dict:
    try:
        j = json.loads(raw)
    except Exception:
        return {"visible": False, "err": "bad_json"}
    thinking_chars = 0
    text_chars = 0
    types = []
    preview = ""
    for c in j.get("content") or []:
        if not isinstance(c, dict):
            continue
        t = c.get("type")
        types.append(t)
        if t == "thinking":
            th = c.get("thinking") or ""
            thinking_chars += len(th)
            if not preview and th:
                preview = th
        if t == "text":
            text_chars += len(c.get("text") or "")
    usage = j.get("usage") or {}
    return {
        "visible": thinking_chars > 0,
        "thinking_chars": thinking_chars,
        "text_chars": text_chars,
        "content_types": types,
        "thinking_preview": safe(preview, 120),
        "usage": usage,
        "reasoning_tokens": int(
            usage.get("reasoning_tokens")
            or (usage.get("output_tokens_details") or {}).get("reasoning_tokens")
            or 0
        ),
    }


def parse_messages_stream(raw: str) -> dict:
    thinking_deltas = 0
    thinking_chars = 0
    text_deltas = 0
    event_types = {}
    usage = None
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
        if et == "content_block_delta":
            delta = obj.get("delta") or {}
            dt = delta.get("type")
            if dt == "thinking_delta":
                thinking_deltas += 1
                thinking_chars += len(delta.get("thinking") or "")
            if dt == "text_delta":
                text_deltas += 1
        if et == "message_delta":
            u = (obj.get("usage") or {})
            if u:
                usage = u
        if et == "message_start":
            msg = obj.get("message") or {}
            if msg.get("usage"):
                usage = msg.get("usage")
    rtok = 0
    if isinstance(usage, dict):
        rtok = int(
            usage.get("reasoning_tokens")
            or (usage.get("output_tokens_details") or {}).get("reasoning_tokens")
            or 0
        )
    return {
        "visible": thinking_chars > 0,
        "thinking_chars": thinking_chars,
        "thinking_deltas": thinking_deltas,
        "text_deltas": text_deltas,
        "reasoning_tokens": rtok,
        "event_top": dict(sorted(event_types.items(), key=lambda x: -x[1])[:12]),
        "raw_thinking_delta": raw.count("thinking_delta"),
    }


def disable_others(token, group_id, keep_ids, results, disabled):
    keep = set(keep_ids)
    items = list_group_accounts(token, group_id)
    n = 0
    for a in items:
        aid = a.get("id")
        if aid in keep:
            continue
        if a.get("schedulable") is False:
            continue
        st, raw = set_schedulable(token, aid, False)
        if st == 200:
            disabled.append((aid, True, group_id))
            n += 1
        else:
            results["setup"].append(
                {"disable_fail": aid, "group": group_id, "http": st, "err": safe(raw, 120)}
            )
    results["setup"].append(
        {"disabled_count": n, "group": group_id, "pin": list(keep), "listed": len(items)}
    )
    print(f"group{group_id}: disabled {n} others, pin={list(keep)}, listed={len(items)}")
    return n


def call_messages(label, key, stream: bool, results_key, results):
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
        apikey=key,
        timeout=150,
        stream=stream,
    )
    dt = round(time.time() - t0, 2)
    row = {"label": label, "http": st, "dt": dt, "bytes": len(raw), "stream": stream}
    if st == 200:
        if stream:
            row.update(parse_messages_stream(raw))
        else:
            row.update(parse_messages_json(raw))
    else:
        row["err"] = safe(raw)
    results[results_key].append(row)
    print(
        f"{label} http={st} vis={row.get('visible')} "
        f"tchars={row.get('thinking_chars')} rtok={row.get('reasoning_tokens')} "
        f"dt={dt} err={row.get('err')}"
    )
    return row


def main():
    results = {
        "setup": [],
        "g2_messages_responses": [],
        "g5_messages_chat": [],
        "usage": [],
        "restore": [],
    }
    disabled = []  # (id, was_true, group)
    proxy_restore = []

    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    if st != 200:
        raise SystemExit(f"login failed {st} {safe(raw)}")
    token = json.loads(raw)["data"]["access_token"]

    st, raw = req("GET", "/api/v1/admin/groups/2", token=token)
    g2 = json.loads(raw).get("data") or {}
    st, raw = req("GET", "/api/v1/admin/groups/5", token=token)
    g5 = json.loads(raw).get("data") or {}
    results["setup"].append(
        {
            "g2_protocol": g2.get("grok_messages_protocol"),
            "g2_name": g2.get("name"),
            "g5_protocol": g5.get("grok_messages_protocol"),
            "g5_name": g5.get("name"),
        }
    )
    print(
        "protocols",
        "g2=",
        g2.get("grok_messages_protocol"),
        "g5=",
        g5.get("grok_messages_protocol"),
    )

    st, raw = req("GET", "/api/v1/admin/groups/2/api-keys", token=token)
    key2 = json.loads(raw)["data"]["items"][0]["key"]
    st, raw = req("GET", "/api/v1/admin/groups/5/api-keys", token=token)
    key5 = json.loads(raw)["data"]["items"][0]["key"]
    print("login ok")

    try:
        for aid in PIN_IDS + GROUP5_EXTRA:
            acc, st, raw = get_account(token, aid)
            if not acc:
                print("missing", aid, st, safe(raw))
                results["setup"].append({"id": aid, "err": safe(raw)})
                continue
            orig_proxy = acc.get("proxy_id")
            orig_sched = acc.get("schedulable")
            proxy_restore.append((aid, orig_proxy))
            row = {
                "id": aid,
                "name": acc.get("name"),
                "orig_proxy": orig_proxy,
                "orig_sched": orig_sched,
                "groups": acc.get("group_ids"),
                "status": acc.get("status"),
            }
            if orig_proxy != 5:
                st2, raw2 = set_proxy(token, aid, 5)
                row["set_proxy5"] = st2
                if st2 != 200:
                    row["set_proxy_err"] = safe(raw2)
            else:
                row["set_proxy5"] = "already"
            if orig_sched is False:
                st3, raw3 = set_schedulable(token, aid, True)
                row["force_sched_true"] = st3
            results["setup"].append(row)
            print(
                f"setup {aid} proxy {orig_proxy}->5 groups={acc.get('group_ids')} sched={orig_sched}"
            )

        # Pin group2 to 1744/1745 only
        disable_others(token, 2, GROUP2_PIN, results, disabled)

        # For group5: if pin accounts not in group5, leave group5 as-is but still try;
        # also try to include 772 if present. Prefer pinning only accounts that are in group5.
        g5_items = list_group_accounts(token, 5)
        g5_ids = {a.get("id") for a in g5_items}
        g5_keep = [aid for aid in PIN_IDS + GROUP5_EXTRA if aid in g5_ids]
        if not g5_keep:
            # no pin accounts in g5 — still run unpinned for baseline
            results["setup"].append({"g5_keep": [], "note": "pin accounts not in group5"})
            print("group5: pin accounts not members; running unpinned baseline")
        else:
            disable_others(token, 5, g5_keep, results, disabled)

        time.sleep(1.5)

        print("\n=== G2 /v1/messages (responses protocol) NONSTREAM x3 ===")
        for i in range(3):
            call_messages(
                f"G2_ns_{i+1}",
                key2,
                False,
                "g2_messages_responses",
                results,
            )
            time.sleep(1.2)

        print("\n=== G2 /v1/messages (responses protocol) STREAM x3 ===")
        for i in range(3):
            call_messages(
                f"G2_s_{i+1}",
                key2,
                True,
                "g2_messages_responses",
                results,
            )
            time.sleep(1.2)

        print("\n=== G5 /v1/messages (chat_completions protocol) NONSTREAM x3 ===")
        for i in range(3):
            call_messages(
                f"G5_ns_{i+1}",
                key5,
                False,
                "g5_messages_chat",
                results,
            )
            time.sleep(1.2)

        print("\n=== G5 /v1/messages (chat_completions protocol) STREAM x3 ===")
        for i in range(3):
            call_messages(
                f"G5_s_{i+1}",
                key5,
                True,
                "g5_messages_chat",
                results,
            )
            time.sleep(1.2)

        st, raw = req("GET", "/api/v1/admin/usage?page_size=20", token=token)
        if st == 200:
            for u in json.loads(raw).get("data", {}).get("items") or []:
                if u.get("group_id") in (2, 5) or u.get("account_id") in set(
                    PIN_IDS + GROUP5_EXTRA
                ):
                    results["usage"].append(
                        {
                            "id": u.get("id"),
                            "account_id": u.get("account_id"),
                            "group_id": u.get("group_id"),
                            "api_key_id": u.get("api_key_id"),
                            "inbound": u.get("inbound_endpoint"),
                            "upstream": u.get("upstream_endpoint"),
                            "stream": u.get("stream"),
                            "reasoning_tokens": u.get("reasoning_tokens"),
                            "model": u.get("model"),
                        }
                    )
        print("\nusage sample:")
        for u in results["usage"][:12]:
            print(u)

    finally:
        print("\n=== RESTORE ===")
        for aid, _, _ in disabled:
            st, raw = set_schedulable(token, aid, True)
            results["restore"].append({"reenable": aid, "http": st})
            if st != 200:
                print("reenable fail", aid, st, safe(raw))
        print(f"reenabled {len(disabled)}")
        for aid, orig in proxy_restore:
            if orig is None:
                continue
            acc, _, _ = get_account(token, aid)
            cur = acc.get("proxy_id") if acc else None
            if cur != orig:
                st, raw = set_proxy(token, aid, orig)
                results["restore"].append(
                    {"proxy": aid, "from": cur, "to": orig, "http": st}
                )
                print(f"restore proxy {aid}: {cur} -> {orig} http={st}")
            else:
                results["restore"].append({"proxy": aid, "unchanged": orig})

    def rate(rows):
        ok = [r for r in rows if r.get("http") == 200]
        if not ok:
            return "0/0"
        vis = sum(1 for r in ok if r.get("visible"))
        return f"{vis}/{len(ok)}"

    summary = {
        "g2_messages_responses_visible": rate(results["g2_messages_responses"]),
        "g5_messages_chat_visible": rate(results["g5_messages_chat"]),
        "g2_ns": rate([r for r in results["g2_messages_responses"] if not r.get("stream")]),
        "g2_s": rate([r for r in results["g2_messages_responses"] if r.get("stream")]),
        "g5_ns": rate([r for r in results["g5_messages_chat"] if not r.get("stream")]),
        "g5_s": rate([r for r in results["g5_messages_chat"] if r.get("stream")]),
        "accounts_used": sorted(
            {u.get("account_id") for u in results["usage"] if u.get("account_id")}
        ),
    }
    results["summary"] = summary
    OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
    print("\nSUMMARY", json.dumps(summary, ensure_ascii=False))
    print("saved", OUT)


if __name__ == "__main__":
    main()
