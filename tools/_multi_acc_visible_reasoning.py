#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Sample visible reasoning across multiple Grok accounts on proxy 5 (美国3)."""
from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from pathlib import Path

BASE = "http://127.0.0.1:18080"
OUT = Path("/tmp/multi_visible_reasoning.json")
MODEL = "grok-4.5"
PROMPT = "Solve step by step: what is 17*19? Show brief reasoning."
# Sample this many distinct accounts via admin probe (quota-costing, opt-in)
PROBE_N = 8
# Also N free LB draws via gateway key (no pin)
GATEWAY_DRAWS = 6


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
            return resp.status, resp.read().decode("utf-8", "replace"), dict(resp.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace"), dict(e.headers)
    except Exception as e:
        return 0, str(e), {}


def safe(s: str, n: int = 160) -> str:
    return (s or "")[:n].encode("unicode_escape").decode("ascii")


def pick_accounts(token: str, group_id: int, limit: int) -> list[dict]:
    st, raw, _ = req(
        "GET",
        f"/api/v1/admin/accounts?group_id={group_id}&page_size=80&status=active",
        token=token,
    )
    if st != 200:
        print("list accounts fail", st, safe(raw, 200))
        return []
    items = json.loads(raw).get("data", {}).get("items") or []
    chosen = []
    for a in items:
        if a.get("status") != "active":
            continue
        if a.get("proxy_id") != 5:
            continue
        if a.get("schedulable") is False:
            continue
        if a.get("temp_unschedulable_until"):
            continue
        # Prefer fresher billing snaps / low used
        extra = a.get("extra") or {}
        snap = extra.get("grok_billing_snapshot") or {}
        used = snap.get("included_used_cents")
        chosen.append(
            {
                "id": a["id"],
                "name": a.get("name") or "",
                "used_cents": used if isinstance(used, int) else 999,
                "session_window_status": a.get("session_window_status"),
            }
        )
    # prefer lower used
    chosen.sort(key=lambda x: (x["used_cents"], x["id"]))
    # diversify: skip first few recently used ids if many
    return chosen[:limit]


def parse_responses_stream(raw: str) -> dict:
    reasoning_chars = 0
    reasoning_deltas = 0
    text_chars = 0
    event_types: dict[str, int] = {}
    usage = None
    encrypted = 0
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
        if "reasoning_summary_text" in et and "delta" in et:
            d = obj.get("delta")
            if isinstance(d, str) and d:
                reasoning_deltas += 1
                reasoning_chars += len(d)
        if et == "response.output_text.delta":
            d = obj.get("delta")
            if isinstance(d, str):
                text_chars += len(d)
        if "encrypted" in et:
            encrypted += 1
        if et == "response.completed":
            usage = (obj.get("response") or {}).get("usage") or obj.get("usage")
    rtok = 0
    if isinstance(usage, dict):
        otd = usage.get("output_tokens_details") or {}
        rtok = int(otd.get("reasoning_tokens") or 0)
    return {
        "reasoning_chars": reasoning_chars,
        "reasoning_deltas": reasoning_deltas,
        "text_chars": text_chars,
        "reasoning_tokens": rtok,
        "encrypted_events": encrypted,
        "event_top": dict(sorted(event_types.items(), key=lambda x: -x[1])[:8]),
        "visible": reasoning_chars > 0,
    }


def parse_chat_json(raw: str) -> dict:
    try:
        j = json.loads(raw)
    except Exception:
        return {"visible": False, "err": "bad_json"}
    msg = ((j.get("choices") or [{}])[0].get("message")) or {}
    rc = msg.get("reasoning_content") or ""
    content = msg.get("content") or ""
    usage = j.get("usage") or {}
    ctd = usage.get("completion_tokens_details") or {}
    return {
        "visible": bool(isinstance(rc, str) and rc.strip()),
        "reasoning_chars": len(rc) if isinstance(rc, str) else 0,
        "reasoning_preview": safe(rc if isinstance(rc, str) else ""),
        "content_chars": len(content) if isinstance(content, str) else 0,
        "reasoning_tokens": int(ctd.get("reasoning_tokens") or 0),
    }


def main():
    st, raw, _ = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    if st != 200:
        print("login fail", st, safe(raw))
        return
    token = json.loads(raw)["data"]["access_token"]
    st, raw, _ = req("GET", "/api/v1/admin/groups/2/api-keys", token=token)
    key2 = json.loads(raw)["data"]["items"][0]["key"]
    print("login+key ok")

    accounts = pick_accounts(token, group_id=2, limit=PROBE_N)
    print(f"picked {len(accounts)} accounts on proxy5 for probe:")
    for a in accounts:
        print(" ", a)

    results = {"probes": [], "gateway_responses": [], "gateway_chat": []}

    # 1) Admin probe per account (forces proxy 5, measures visible without leaking text)
    print("\n=== ADMIN PROBES proxy=5 ===")
    for a in accounts:
        aid = a["id"]
        t0 = time.time()
        st, raw, _ = req(
            "POST",
            "/api/v1/admin/proxies/5/grok-reasoning-probe",
            {"account_id": aid, "confirm_quota_cost": True},
            token=token,
            timeout=120,
        )
        dt = round(time.time() - t0, 2)
        row = {"account_id": aid, "name": a["name"], "http": st, "dt": dt}
        if st == 200:
            try:
                data = json.loads(raw).get("data") or json.loads(raw)
            except Exception:
                data = {"raw": safe(raw, 300)}
            if isinstance(data, dict):
                for k in [
                    "status",
                    "has_visible_reasoning",
                    "visible_reasoning_chars",
                    "has_encrypted_reasoning",
                    "reasoning_tokens",
                    "output_tokens",
                    "http_status",
                    "latency_ms",
                    "model",
                    "message",
                    "stream_completed",
                    "proxy_id",
                    "account_id",
                ]:
                    if k in data:
                        row[k] = data[k]
        else:
            row["err"] = safe(raw, 240)
        results["probes"].append(row)
        print(
            f"probe acc={aid} http={st} status={row.get('status')} "
            f"visible={row.get('has_visible_reasoning')} chars={row.get('visible_reasoning_chars')} "
            f"rtok={row.get('reasoning_tokens')} enc={row.get('has_encrypted_reasoning')} dt={dt}"
        )
        time.sleep(0.8)

    # 2) Gateway multi-draw /v1/responses stream (LB picks accounts)
    print("\n=== GATEWAY /v1/responses stream x", GATEWAY_DRAWS, "===")
    for i in range(GATEWAY_DRAWS):
        payload = {
            "model": MODEL,
            "input": PROMPT,
            "reasoning": {"effort": "high"},
            "stream": True,
        }
        t0 = time.time()
        st, raw, _ = req(
            "POST",
            "/v1/responses",
            data=payload,
            apikey=key2,
            timeout=150,
            stream=True,
        )
        dt = round(time.time() - t0, 2)
        row = {"i": i + 1, "http": st, "dt": dt, "bytes": len(raw)}
        if st == 200:
            row.update(parse_responses_stream(raw))
        else:
            row["err"] = safe(raw, 200)
        results["gateway_responses"].append(row)
        print(
            f"R{i+1} http={st} visible={row.get('visible')} rchars={row.get('reasoning_chars')} "
            f"rtok={row.get('reasoning_tokens')} text={row.get('text_chars')} dt={dt}"
        )
        time.sleep(1.0)

    # 3) Gateway multi-draw chat completions nonstream
    print("\n=== GATEWAY /v1/chat/completions x", GATEWAY_DRAWS, "===")
    for i in range(GATEWAY_DRAWS):
        payload = {
            "model": MODEL,
            "messages": [{"role": "user", "content": PROMPT}],
            "stream": False,
            "reasoning_effort": "high",
        }
        t0 = time.time()
        st, raw, _ = req(
            "POST", "/v1/chat/completions", data=payload, apikey=key2, timeout=120
        )
        dt = round(time.time() - t0, 2)
        row = {"i": i + 1, "http": st, "dt": dt, "bytes": len(raw)}
        if st == 200:
            row.update(parse_chat_json(raw))
        else:
            row["err"] = safe(raw, 200)
        results["gateway_chat"].append(row)
        print(
            f"C{i+1} http={st} visible={row.get('visible')} rchars={row.get('reasoning_chars')} "
            f"rtok={row.get('reasoning_tokens')} preview={row.get('reasoning_preview')} dt={dt}"
        )
        time.sleep(1.0)

    # usage attribution for recent gateway calls
    st, raw, _ = req("GET", "/api/v1/admin/usage?page_size=20", token=token)
    usage_rows = []
    if st == 200:
        for u in json.loads(raw).get("data", {}).get("items") or []:
            usage_rows.append(
                {
                    "id": u.get("id"),
                    "account_id": u.get("account_id"),
                    "group_id": u.get("group_id"),
                    "api_key_id": u.get("api_key_id"),
                    "model": u.get("model"),
                    "stream": u.get("stream"),
                    "inbound": u.get("inbound_endpoint"),
                    "upstream": u.get("upstream_endpoint"),
                    "reasoning_tokens": u.get("reasoning_tokens"),
                    "input_tokens": u.get("input_tokens"),
                    "output_tokens": u.get("output_tokens"),
                }
            )
    results["recent_usage"] = usage_rows[:15]

    # summary
    def rate(rows, key="visible"):
        ok = [r for r in rows if r.get("http") == 200]
        if not ok:
            return "0/0"
        vis = sum(1 for r in ok if r.get(key) or r.get("has_visible_reasoning"))
        return f"{vis}/{len(ok)}"

    summary = {
        "probe_visible_rate": rate(results["probes"], "has_visible_reasoning"),
        "responses_visible_rate": rate(results["gateway_responses"], "visible"),
        "chat_visible_rate": rate(results["gateway_chat"], "visible"),
    }
    results["summary"] = summary
    OUT.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
    print("\n=== SUMMARY ===")
    print(json.dumps(summary, ensure_ascii=False))
    print("saved", OUT)


if __name__ == "__main__":
    main()
