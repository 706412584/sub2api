#!/usr/bin/env python3
"""Reproduce: group2 key + account 772 + proxy 5 (美国3) + /v1/responses visible reasoning."""
from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from pathlib import Path

BASE = "http://127.0.0.1:18080"
OUT = Path("/tmp")


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
        resp = urllib.request.urlopen(r, timeout=timeout)
        raw = resp.read().decode("utf-8", "replace")
        return resp.status, raw, dict(resp.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace"), dict(e.headers)


def extract_reasoning(obj, path="$"):
    found = []
    if isinstance(obj, dict):
        for k, v in obj.items():
            lk = k.lower()
            if any(x in lk for x in ("reasoning", "thinking", "summary")):
                if isinstance(v, str) and v.strip():
                    found.append((path + "." + k, v[:500], len(v)))
                elif isinstance(v, (list, dict)):
                    found.extend(extract_reasoning(v, path + "." + k))
            else:
                found.extend(extract_reasoning(v, path + "." + k))
    elif isinstance(obj, list):
        for i, it in enumerate(obj):
            found.extend(extract_reasoning(it, f"{path}[{i}]"))
    return found


def main():
    st, raw, _ = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    token = json.loads(raw)["data"]["access_token"]
    print("login ok")

    st, raw, _ = req("GET", "/api/v1/admin/groups/2/api-keys", token=token)
    items = json.loads(raw)["data"]["items"]
    key = items[0]["key"]
    print("key_id", items[0]["id"], "key_prefix", key[:12])

    st, raw, _ = req("GET", "/api/v1/admin/accounts/772", token=token)
    acc = json.loads(raw)["data"]
    orig_proxy = acc.get("proxy_id")
    print(
        "acc772 proxy_id",
        orig_proxy,
        "status",
        acc.get("status"),
        "group_ids",
        acc.get("group_ids"),
    )
    if orig_proxy != 5:
        st, raw, _ = req("PUT", "/api/v1/admin/accounts/772", {"proxy_id": 5}, token=token)
        print("set proxy5", st, raw[:200])
        st, raw, _ = req("GET", "/api/v1/admin/accounts/772", token=token)
        print("after proxy", json.loads(raw)["data"].get("proxy_id"))

    model = "grok-4"
    st, raw, _ = req("GET", "/v1/models", apikey=key, timeout=30)
    print("models", st, raw[:250])
    try:
        mj = json.loads(raw)
        mids = [m.get("id") for m in (mj.get("data") or []) if isinstance(m, dict) and m.get("id")]
        print("model_ids sample", mids[:15])
        for cand in ["grok-4", "grok-4-0709", "grok-3", "grok-4-latest", "grok-2"]:
            if cand in mids:
                model = cand
                break
        else:
            for mid in mids:
                if "grok" in mid:
                    model = mid
                    break
            if mids and model not in mids and "grok" not in model:
                model = mids[0]
    except Exception as e:
        print("models parse err", e)
    print("using model", model)

    payload = {
        "model": model,
        "input": "Solve step by step: what is 17*19? Show brief reasoning.",
        "reasoning": {"effort": "high"},
        "stream": False,
    }

    print("\n=== NON-STREAM /v1/responses ===")
    t0 = time.time()
    st, raw, _ = req("POST", "/v1/responses", data=payload, apikey=key, timeout=180)
    dt = time.time() - t0
    print("status", st, "dt", round(dt, 2), "bytes", len(raw))
    (OUT / "repro_responses_nonstream.json").write_text(raw, encoding="utf-8")
    if st != 200:
        print("ERR", raw[:1000])
    else:
        j = json.loads(raw)
        usage = j.get("usage") or {}
        print("usage", json.dumps(usage, ensure_ascii=False)[:500])
        out_text = []
        for o in j.get("output") or []:
            if not isinstance(o, dict):
                continue
            if o.get("type") == "message":
                for c in o.get("content") or []:
                    if isinstance(c, dict) and c.get("type") in ("output_text", "text"):
                        out_text.append(c.get("text") or "")
            if o.get("type") in ("reasoning", "reasoning_text"):
                print("reasoning item keys", list(o.keys()))
                s = o.get("summary") or o.get("content") or o.get("text")
                print("reasoning payload type", type(s).__name__, repr(str(s)[:300]) if s else None)
        print("output_text", "".join(out_text)[:400])
        rs = extract_reasoning(j)
        print("reasoning_hits", len(rs))
        total_chars = 0
        for p, v, n in rs[:20]:
            total_chars += n
            print(f"  {p} len={n}: {v[:160]!r}")
        print("reasoning_text_total_chars_approx", total_chars)

    print("\n=== STREAM /v1/responses ===")
    payload_s = dict(payload)
    payload_s["stream"] = True
    t0 = time.time()
    st, raw, hdrs = req(
        "POST", "/v1/responses", data=payload_s, apikey=key, timeout=180, stream=True
    )
    dt = time.time() - t0
    print(
        "status",
        st,
        "dt",
        round(dt, 2),
        "bytes",
        len(raw),
        "ctype",
        hdrs.get("Content-Type") or hdrs.get("content-type"),
    )
    (OUT / "repro_responses_stream.txt").write_text(raw, encoding="utf-8")
    if st != 200:
        print("ERR", raw[:1000])
    else:
        event_types = {}
        reasoning_deltas = []
        text_deltas = []
        cur_event = None
        for line in raw.splitlines():
            if line.startswith("event:"):
                cur_event = line[6:].strip()
                continue
            if not line.startswith("data:"):
                continue
            data = line[5:].strip()
            if data == "[DONE]":
                continue
            try:
                obj = json.loads(data)
            except Exception:
                continue
            et = obj.get("type") or cur_event or "unknown"
            event_types[et] = event_types.get(et, 0) + 1
            if "reasoning" in et:
                d = obj.get("delta")
                if isinstance(d, str) and d:
                    reasoning_deltas.append(d)
                t = obj.get("text")
                if isinstance(t, str) and t and not d:
                    reasoning_deltas.append(t)
            if "output_text" in et or et.endswith("text.delta"):
                d = obj.get("delta")
                if isinstance(d, str):
                    text_deltas.append(d)
        print(
            "event_types",
            dict(sorted(event_types.items(), key=lambda x: -x[1])[:25]),
        )
        rtext = "".join(reasoning_deltas)
        ttext = "".join(text_deltas)
        print(
            "reasoning_delta_count",
            len(reasoning_deltas),
            "reasoning_chars",
            len(rtext),
        )
        print("reasoning_preview", rtext[:500])
        print("text_delta_count", len(text_deltas), "text_chars", len(ttext))
        print("text_preview", ttext[:300])
        for pat in [
            "reasoning_summary_text",
            "reasoning_content",
            "thinking_delta",
            "response.reasoning",
        ]:
            print("raw_count", pat, raw.count(pat))

    # restore proxy if we changed it
    if orig_proxy is not None and orig_proxy != 5:
        st, raw, _ = req(
            "PUT", "/api/v1/admin/accounts/772", {"proxy_id": orig_proxy}, token=token
        )
        print("restore proxy", orig_proxy, st)
    else:
        print("proxy left as", orig_proxy, "(already 5)")

    print("\nDONE")


if __name__ == "__main__":
    main()
