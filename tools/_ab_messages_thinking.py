#!/usr/bin/env python3
# -*- coding: utf-8 -*-
from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from pathlib import Path

BASE = "http://127.0.0.1:18080"
OUT = Path("/tmp")


def req(method, path, data=None, token=None, apikey=None, timeout=180):
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if apikey:
        headers["Authorization"] = f"Bearer {apikey}"
    body = None if data is None else json.dumps(data, ensure_ascii=False).encode("utf-8")
    r = urllib.request.Request(BASE + path, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")


def safe(s: str) -> str:
    return (s or "").encode("unicode_escape").decode("ascii")


def main():
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

    prompt = "What is 17*19? Brief steps."
    model = "grok-4.5"
    payload = {
        "model": model,
        "max_tokens": 512,
        "messages": [{"role": "user", "content": prompt}],
        "stream": False,
        "thinking": {"type": "enabled", "budget_tokens": 1024},
    }

    for label, key in [("G2_messages_responses_proto", key2), ("G5_messages_chat_proto", key5)]:
        print(f"\n=== {label} ===")
        t0 = time.time()
        st, raw = req("POST", "/v1/messages", data=payload, apikey=key, timeout=120)
        print("status", st, "dt", round(time.time() - t0, 2), "bytes", len(raw))
        (OUT / f"ab_{label}.json").write_text(raw, encoding="utf-8")
        if st != 200:
            print("ERR", safe(raw[:500]))
            continue
        j = json.loads(raw)
        print("usage", j.get("usage"))
        types = [c.get("type") for c in (j.get("content") or []) if isinstance(c, dict)]
        print("content_types", types)
        for c in j.get("content") or []:
            if not isinstance(c, dict):
                continue
            t = c.get("type")
            txt = c.get("text") or c.get("thinking") or ""
            print(f"  block type={t} len={len(txt)} preview={safe(txt[:220])}")

    # stream g5 messages once
    print("\n=== G5_messages_chat_proto STREAM ===")
    payload_s = dict(payload)
    payload_s["stream"] = True
    t0 = time.time()
    st, raw = req("POST", "/v1/messages", data=payload_s, apikey=key5, timeout=180)
    print("status", st, "dt", round(time.time() - t0, 2), "bytes", len(raw))
    (OUT / "ab_G5_messages_stream.txt").write_text(raw, encoding="utf-8")
    if st != 200:
        print("ERR", safe(raw[:500]))
    else:
        thinking_deltas = 0
        thinking_chars = 0
        text_deltas = 0
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
            if et == "content_block_delta":
                delta = obj.get("delta") or {}
                dt = delta.get("type")
                if dt == "thinking_delta":
                    thinking_deltas += 1
                    thinking_chars += len(delta.get("thinking") or "")
                if dt == "text_delta":
                    text_deltas += 1
        print("event_types", dict(sorted(event_types.items(), key=lambda x: -x[1])[:20]))
        print(
            "thinking_deltas",
            thinking_deltas,
            "thinking_chars",
            thinking_chars,
            "text_deltas",
            text_deltas,
        )
        print("raw_count thinking_delta", raw.count("thinking_delta"))
        print("raw_count thinking", raw.count('"thinking"'))

    # usage recent
    st, raw = req("GET", "/api/v1/admin/usage?page_size=6", token=token)
    items = json.loads(raw)["data"]["items"]
    print("\n=== recent usage ===")
    for u in items:
        row = {
            k: u.get(k)
            for k in [
                "id",
                "api_key_id",
                "account_id",
                "group_id",
                "model",
                "stream",
                "input_tokens",
                "output_tokens",
                "reasoning_tokens",
                "path",
                "endpoint",
                "upstream_endpoint",
                "request_path",
                "platform",
            ]
            if u.get(k) is not None
        }
        # catch alternate field names
        for k, v in u.items():
            if any(x in k.lower() for x in ["end", "path", "reason", "account", "group", "key", "model"]):
                if k not in row:
                    row[k] = v
        print(row)


if __name__ == "__main__":
    main()
