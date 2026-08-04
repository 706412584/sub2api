#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Random sample of quota-positive Grok accounts; retest visible reasoning."""
from __future__ import annotations

import json
import random
import time
import urllib.error
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

BASE = "http://127.0.0.1:18080"
OUT = Path("/tmp/random_quota_visible_retest.json")
MODEL = "grok-4.5"
PROMPT = "Solve step by step: what is 17*19? Show brief reasoning."
SAMPLE_N = 8
PROXY_ID = 5  # 美国3 for probe consistency
RNG = random.Random(20260802)


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


def parse_ts(v):
    if not v:
        return None
    if isinstance(v, (int, float)):
        return float(v)
    s = str(v).replace("Z", "+00:00")
    try:
        return datetime.fromisoformat(s).timestamp()
    except Exception:
        return None


def remaining_info(acc: dict) -> dict:
    extra = acc.get("extra") or {}
    snap = extra.get("grok_usage_snapshot") or {}
    reqs = snap.get("requests") or {}
    toks = snap.get("tokens") or {}
    headers = snap.get("headers") or {}
    rem_req = reqs.get("remaining")
    if rem_req is None:
        try:
            rem_req = int(headers.get("x-ratelimit-remaining-requests"))
        except Exception:
            rem_req = None
    rem_tok = toks.get("remaining")
    if rem_tok is None:
        try:
            rem_tok = int(headers.get("x-ratelimit-remaining-tokens"))
        except Exception:
            rem_tok = None
    blocked_until = parse_ts(extra.get("grok_quota_blocked_until"))
    rate_reset = parse_ts(acc.get("rate_limit_reset_at"))
    now = time.time()
    return {
        "rem_req": rem_req,
        "rem_tok": rem_tok,
        "headers_observed": bool(snap.get("headers_observed")),
        "snap_status": snap.get("status_code"),
        "snap_updated_at": snap.get("updated_at") or snap.get("last_headers_seen_at"),
        "quota_blocked_reason": extra.get("grok_quota_blocked_reason"),
        "quota_blocked_active": bool(blocked_until and blocked_until > now),
        "rate_limited_active": bool(rate_reset and rate_reset > now),
        "rate_limit_reset_at": acc.get("rate_limit_reset_at"),
    }


def is_quota_positive(acc: dict, info: dict) -> bool:
    if acc.get("status") != "active":
        return False
    if acc.get("platform") != "grok":
        return False
    if acc.get("type") != "oauth":
        return False
    if acc.get("schedulable") is False:
        return False
    if info.get("quota_blocked_active"):
        return False
    if info.get("rate_limited_active"):
        return False
    # Prefer observed remaining > 0; also accept recent 200 snap with remaining None only if headers say so
    rem = info.get("rem_req")
    if rem is None:
        return False
    try:
        return int(rem) > 0
    except Exception:
        return False


def list_group_accounts(token, group_id, max_pages=12):
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


def parse_responses_stream(raw: str) -> dict:
    reasoning_chars = 0
    reasoning_deltas = 0
    text_chars = 0
    usage = None
    event_types = {}
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
        if et == "response.reasoning_summary_text.delta":
            d = obj.get("delta")
            if isinstance(d, str) and d:
                reasoning_deltas += 1
                reasoning_chars += len(d)
        if et in ("response.reasoning_text.delta",):
            d = obj.get("delta")
            if isinstance(d, str) and d:
                reasoning_deltas += 1
                reasoning_chars += len(d)
        if et == "response.output_text.delta":
            d = obj.get("delta")
            if isinstance(d, str):
                text_chars += len(d)
        if et == "response.completed":
            usage = (obj.get("response") or {}).get("usage") or obj.get("usage")
    rtok = 0
    if isinstance(usage, dict):
        rtok = int((usage.get("output_tokens_details") or {}).get("reasoning_tokens") or 0)
    return {
        "visible": reasoning_chars > 0,
        "reasoning_chars": reasoning_chars,
        "reasoning_deltas": reasoning_deltas,
        "text_chars": text_chars,
        "reasoning_tokens": rtok,
        "event_top": dict(sorted(event_types.items(), key=lambda x: -x[1])[:8]),
    }


def parse_chat_json(raw: str) -> dict:
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
        "reasoning_preview": safe(rc, 100),
        "content_chars": len(content),
        "reasoning_tokens": rtok,
    }


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
        "thinking_preview": safe(preview, 100),
        "usage": usage,
    }


def set_sched(token, aid, v):
    return req(
        "POST",
        f"/api/v1/admin/accounts/{aid}/schedulable",
        {"schedulable": v},
        token=token,
    )


def get_acc(token, aid):
    st, raw = req("GET", f"/api/v1/admin/accounts/{aid}", token=token)
    if st != 200:
        return None
    return json.loads(raw)["data"]


def main():
    results = {
        "sampled": [],
        "probes": [],
        "gateway": [],
        "usage": [],
        "restore": [],
        "pool_stats": {},
    }
    disabled = []

    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    token = json.loads(raw)["data"]["access_token"]
    st, raw = req("GET", "/api/v1/admin/groups/2/api-keys", token=token)
    key2 = json.loads(raw)["data"]["items"][0]["key"]

    # Build quota-positive pool from group2 (largest Grok pool)
    items = list_group_accounts(token, 2)
    candidates = []
    for a in items:
        info = remaining_info(a)
        if is_quota_positive(a, info):
            candidates.append(
                {
                    "id": a["id"],
                    "name": a.get("name"),
                    "proxy_id": a.get("proxy_id"),
                    "schedulable": a.get("schedulable"),
                    **info,
                }
            )
    RNG.shuffle(candidates)
    sample = candidates[:SAMPLE_N]
    results["pool_stats"] = {
        "group2_active_listed": len(items),
        "quota_positive": len(candidates),
        "sampled": len(sample),
        "sample_ids": [s["id"] for s in sample],
    }
    results["sampled"] = sample
    print(
        f"pool group2 listed={len(items)} quota_positive={len(candidates)} sample={ [s['id'] for s in sample] }"
    )
    for s in sample:
        print(
            f"  {s['id']} rem_req={s['rem_req']} rem_tok={s['rem_tok']} proxy={s['proxy_id']} snap={s['snap_updated_at']}"
        )

    if not sample:
        OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
        print("no candidates")
        return

    try:
        # 1) Admin probes on each sampled account via proxy 5 (isolates account credential)
        print("\n=== PROBES proxy5 ===")
        for s in sample:
            aid = s["id"]
            t0 = time.time()
            st, raw = req(
                "POST",
                f"/api/v1/admin/proxies/{PROXY_ID}/grok-reasoning-probe",
                {"account_id": aid, "confirm_quota_cost": True},
                token=token,
                timeout=120,
            )
            dt = round(time.time() - t0, 2)
            row = {"account_id": aid, "http": st, "dt": dt, "proxy_id": PROXY_ID}
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
            else:
                row["err"] = safe(raw)
            results["probes"].append(row)
            print(
                f"probe {aid} http={st} status={row.get('status')} vis={row.get('visible')} "
                f"chars={row.get('visible_reasoning_chars')} rtok={row.get('reasoning_tokens')} "
                f"msg={safe(str(row.get('message') or row.get('err') or ''), 80)} dt={dt}"
            )
            time.sleep(0.6)

        # 2) Gateway: pin one visible-or-first sample account at a time for chat + messages
        # Pick up to 3 accounts: prefer probe-visible, else first 3 quota samples
        probe_vis = [p["account_id"] for p in results["probes"] if p.get("visible")]
        gateway_targets = probe_vis[:3] or [s["id"] for s in sample[:3]]
        print(f"\n=== GATEWAY pin targets {gateway_targets} ===")

        # Snapshot schedulable of all group2 to restore only what we disable
        all_ids = [a["id"] for a in items]
        # Disable everyone except current target each round
        for target in gateway_targets:
            # enable target
            acc = get_acc(token, target)
            if not acc:
                results["gateway"].append({"account_id": target, "err": "missing"})
                continue
            if acc.get("schedulable") is False:
                set_sched(token, target, True)

            # disable others (only currently schedulable)
            disabled_this = []
            for a in items:
                aid = a["id"]
                if aid == target:
                    continue
                # refresh would be slow; use list snapshot sched flag then set
                if a.get("schedulable") is False:
                    continue
                st, raw = set_sched(token, aid, False)
                if st == 200:
                    disabled_this.append(aid)
                    disabled.append(aid)
            # also ensure non-listed? skip
            print(f"pinned {target}, disabled {len(disabled_this)}")
            time.sleep(1.0)

            # chat nonstream
            payload_chat = {
                "model": MODEL,
                "messages": [{"role": "user", "content": PROMPT}],
                "stream": False,
            }
            t0 = time.time()
            st, raw = req(
                "POST", "/v1/chat/completions", data=payload_chat, apikey=key2, timeout=120
            )
            dt = round(time.time() - t0, 2)
            row = {
                "account_id": target,
                "path": "chat",
                "http": st,
                "dt": dt,
                "bytes": len(raw),
            }
            if st == 200:
                row.update(parse_chat_json(raw))
            else:
                row["err"] = safe(raw)
            results["gateway"].append(row)
            print(
                f"chat {target} http={st} vis={row.get('visible')} rchars={row.get('reasoning_chars')} "
                f"rtok={row.get('reasoning_tokens')} dt={dt} err={row.get('err')}"
            )
            time.sleep(0.8)

            # messages nonstream (group2 = responses protocol)
            payload_msg = {
                "model": MODEL,
                "max_tokens": 512,
                "messages": [{"role": "user", "content": PROMPT}],
                "stream": False,
                "thinking": {"type": "enabled", "budget_tokens": 1024},
            }
            t0 = time.time()
            st, raw = req("POST", "/v1/messages", data=payload_msg, apikey=key2, timeout=150)
            dt = round(time.time() - t0, 2)
            row = {
                "account_id": target,
                "path": "messages",
                "http": st,
                "dt": dt,
                "bytes": len(raw),
            }
            if st == 200:
                row.update(parse_messages_json(raw))
            else:
                row["err"] = safe(raw)
            results["gateway"].append(row)
            print(
                f"messages {target} http={st} vis={row.get('visible')} tchars={row.get('thinking_chars')} "
                f"types={row.get('content_types')} dt={dt} err={row.get('err')}"
            )

            # re-enable disabled_this before next pin (keep pool healthy between targets)
            for aid in disabled_this:
                set_sched(token, aid, True)
            # remove from global disabled list since restored
            disabled = [x for x in disabled if x not in disabled_this]
            time.sleep(0.8)

        st, raw = req("GET", "/api/v1/admin/usage?page_size=20", token=token)
        if st == 200:
            sample_ids = {s["id"] for s in sample}
            for u in json.loads(raw).get("data", {}).get("items") or []:
                if u.get("account_id") in sample_ids or u.get("group_id") == 2:
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

    finally:
        print("\n=== RESTORE ===")
        # re-enable any still disabled
        uniq = sorted(set(disabled))
        for aid in uniq:
            st, raw = set_sched(token, aid, True)
            results["restore"].append({"reenable": aid, "http": st})
        print(f"reenabled leftover {len(uniq)}")

        # safety: re-enable all sample targets
        for s in sample:
            set_sched(token, s["id"], True)

    def rate(rows, vis_key="visible"):
        ok = [r for r in rows if r.get("http") == 200]
        if not ok:
            return "0/0"
        vis = sum(1 for r in ok if r.get(vis_key) or r.get("has_visible_reasoning"))
        return f"{vis}/{len(ok)}"

    chat_rows = [r for r in results["gateway"] if r.get("path") == "chat"]
    msg_rows = [r for r in results["gateway"] if r.get("path") == "messages"]
    summary = {
        "pool_quota_positive": results["pool_stats"].get("quota_positive"),
        "sampled": results["pool_stats"].get("sample_ids"),
        "probe_visible": rate(results["probes"]),
        "probe_statuses": {
            p.get("account_id"): p.get("status") for p in results["probes"]
        },
        "chat_visible": rate(chat_rows),
        "messages_visible": rate(msg_rows),
        "gateway_accounts": sorted(
            {r.get("account_id") for r in results["gateway"] if r.get("account_id")}
        ),
    }
    results["summary"] = summary
    OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
    print("\nSUMMARY", json.dumps(summary, ensure_ascii=False))
    print("saved", OUT)


if __name__ == "__main__":
    main()
