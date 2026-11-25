#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Import sample Grok accounts+US3 proxy from custom 18080 to official 18090, then A/B visible reasoning."""
from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from pathlib import Path

CUSTOM = "http://127.0.0.1:18080"
OFFICIAL = "http://127.0.0.1:18090"
OUT = Path("/tmp/official_vs_custom_visible_ab.json")
SAMPLE_IDS = "1744,1745,772,286,834,287,289,494"  # mix of known + proxy5 actives
PROMPT = "Solve step by step: what is 17*19? Show brief reasoning."
MODEL = "grok-4.5"
DRAWS = 6


def req(base, method, path, data=None, token=None, apikey=None, timeout=180, stream=False):
    headers = {"Content-Type": "application/json", "Accept": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if apikey:
        headers["Authorization"] = f"Bearer {apikey}"
        if stream:
            headers["Accept"] = "text/event-stream"
    body = None if data is None else json.dumps(data, ensure_ascii=False).encode("utf-8")
    r = urllib.request.Request(base + path, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, str(e)


def safe(s, n=160):
    return (s or "")[:n].encode("unicode_escape").decode("ascii")


def login(base, email, password):
    st, raw = req(base, "POST", "/api/v1/auth/login", {"email": email, "password": password})
    if st != 200:
        raise RuntimeError(f"login {base} {st} {safe(raw)}")
    return json.loads(raw)["data"]["access_token"]


def parse_responses_stream(raw: str) -> dict:
    rchars = rdelta = tchars = rtok = 0
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
        et = obj.get("type") or ""
        if et == "response.reasoning_summary_text.delta":
            d = obj.get("delta")
            if isinstance(d, str) and d:
                rdelta += 1
                rchars += len(d)
        if et == "response.output_text.delta":
            d = obj.get("delta")
            if isinstance(d, str):
                tchars += len(d)
        if et == "response.completed":
            usage = (obj.get("response") or {}).get("usage") or obj.get("usage") or {}
            rtok = int((usage.get("output_tokens_details") or {}).get("reasoning_tokens") or 0)
    return {
        "visible": rchars > 0,
        "reasoning_chars": rchars,
        "reasoning_deltas": rdelta,
        "text_chars": tchars,
        "reasoning_tokens": rtok,
    }


def parse_chat(raw: str) -> dict:
    try:
        j = json.loads(raw)
    except Exception:
        return {"visible": False, "err": "bad_json"}
    msg = ((j.get("choices") or [{}])[0].get("message")) or {}
    rc = msg.get("reasoning_content") or ""
    usage = j.get("usage") or {}
    rtok = int((usage.get("completion_tokens_details") or {}).get("reasoning_tokens") or 0)
    return {
        "visible": bool(isinstance(rc, str) and rc.strip()),
        "reasoning_chars": len(rc) if isinstance(rc, str) else 0,
        "reasoning_tokens": rtok,
        "preview": safe(rc if isinstance(rc, str) else "", 80),
    }


def ensure_api_key(base, token, group_id, name="ab-visible-test"):
    st, raw = req(base, "GET", f"/api/v1/admin/groups/{group_id}/api-keys", token=token)
    if st == 200:
        items = (json.loads(raw).get("data") or {}).get("items") or []
        if items:
            return items[0].get("key") or items[0].get("api_key")
    # create
    st, raw = req(
        base,
        "POST",
        f"/api/v1/admin/groups/{group_id}/api-keys",
        {"name": name, "limit": 0},
        token=token,
    )
    if st not in (200, 201):
        # try alternate payload
        st, raw = req(
            base,
            "POST",
            "/api/v1/admin/api-keys",
            {"name": name, "group_id": group_id},
            token=token,
        )
    if st not in (200, 201):
        raise RuntimeError(f"create key fail {st} {safe(raw)}")
    data = json.loads(raw).get("data") or {}
    return data.get("key") or data.get("api_key") or data.get("token")


def run_draws(base, apikey, label):
    rows = {"responses": [], "chat": []}
    print(f"\n=== {label} /v1/responses x{DRAWS} ===")
    for i in range(DRAWS):
        payload = {
            "model": MODEL,
            "input": PROMPT,
            "reasoning": {"effort": "high"},
            "stream": True,
        }
        t0 = time.time()
        st, raw = req(base, "POST", "/v1/responses", payload, apikey=apikey, timeout=150, stream=True)
        dt = round(time.time() - t0, 2)
        row = {"i": i + 1, "http": st, "dt": dt}
        if st == 200:
            row.update(parse_responses_stream(raw))
        else:
            row["err"] = safe(raw)
        rows["responses"].append(row)
        print(
            f"R{i+1} http={st} vis={row.get('visible')} rchars={row.get('reasoning_chars')} "
            f"rtok={row.get('reasoning_tokens')} dt={dt}"
        )
        time.sleep(0.8)

    print(f"\n=== {label} /v1/chat/completions x{DRAWS} ===")
    for i in range(DRAWS):
        payload = {
            "model": MODEL,
            "messages": [{"role": "user", "content": PROMPT}],
            "stream": False,
            "reasoning_effort": "high",
        }
        t0 = time.time()
        st, raw = req(base, "POST", "/v1/chat/completions", payload, apikey=apikey, timeout=120)
        dt = round(time.time() - t0, 2)
        row = {"i": i + 1, "http": st, "dt": dt}
        if st == 200:
            row.update(parse_chat(raw))
        else:
            row["err"] = safe(raw)
        rows["chat"].append(row)
        print(
            f"C{i+1} http={st} vis={row.get('visible')} rchars={row.get('reasoning_chars')} "
            f"rtok={row.get('reasoning_tokens')} dt={dt}"
        )
        time.sleep(0.8)
    return rows


def rate(rows):
    ok = [r for r in rows if r.get("http") == 200]
    if not ok:
        return "0/0"
    vis = sum(1 for r in ok if r.get("visible"))
    return f"{vis}/{len(ok)}"


def main():
    out = {"import": {}, "official": {}, "custom": {}, "summary": {}}
    ctoken = login(CUSTOM, "admin@local.test", "12345678")
    otoken = login(OFFICIAL, "admin@official.local", "12345678")
    print("logins ok")

    # export sample from custom (credentials included in admin export — keep local only)
    st, raw = req(
        CUSTOM,
        "GET",
        f"/api/v1/admin/accounts/data?ids={SAMPLE_IDS}&include_proxies=true",
        token=ctoken,
    )
    if st != 200:
        raise RuntimeError(f"export fail {st} {safe(raw)}")
    payload = json.loads(raw)["data"]
    # strip secrets from log; keep full payload for import only in memory
    n_acc = len(payload.get("accounts") or [])
    n_px = len(payload.get("proxies") or [])
    print(f"exported accounts={n_acc} proxies={n_px} (credentials not printed)")
    out["import"]["exported_accounts"] = n_acc
    out["import"]["exported_proxies"] = n_px
    out["import"]["proxy_names"] = [p.get("name") for p in (payload.get("proxies") or [])]

    # find official grok group
    st, raw = req(OFFICIAL, "GET", "/api/v1/admin/groups?page_size=20", token=otoken)
    groups = json.loads(raw)["data"]["items"]
    grok_gid = next((g["id"] for g in groups if g.get("platform") == "grok"), None)
    if not grok_gid:
        raise RuntimeError("official has no grok group")
    print("official grok group", grok_gid)

    # import into official, bind to grok group
    st, raw = req(
        OFFICIAL,
        "POST",
        "/api/v1/admin/accounts/data",
        {
            "data": payload,
            "group_ids": [grok_gid],
            "skip_default_group_bind": True,
        },
        token=otoken,
        timeout=180,
    )
    print("import", st, safe(raw, 400))
    if st == 200:
        out["import"]["result"] = json.loads(raw).get("data")
    else:
        out["import"]["error"] = safe(raw, 500)
        # continue if accounts already exist

    # ensure keys
    okey = ensure_api_key(OFFICIAL, otoken, grok_gid, "official-ab-visible")
    # custom group 2
    ckey = ensure_api_key(CUSTOM, ctoken, 2, "custom-ab-visible")
    print("keys ready (not printed)")

    out["official"] = run_draws(OFFICIAL, okey, "OFFICIAL-18090")
    out["custom"] = run_draws(CUSTOM, ckey, "CUSTOM-18080")

    # usage account attribution on both
    for label, base, token in [
        ("official", OFFICIAL, otoken),
        ("custom", CUSTOM, ctoken),
    ]:
        st, raw = req(base, "GET", "/api/v1/admin/usage?page_size=20", token=token)
        usage = []
        if st == 200:
            for u in (json.loads(raw).get("data") or {}).get("items") or []:
                usage.append(
                    {
                        "account_id": u.get("account_id"),
                        "inbound": u.get("inbound_endpoint"),
                        "upstream": u.get("upstream_endpoint"),
                        "reasoning_tokens": u.get("reasoning_tokens"),
                        "stream": u.get("stream"),
                        "model": u.get("model"),
                    }
                )
        out.setdefault(label, {})["recent_usage"] = usage[:12]

    out["summary"] = {
        "official_responses_visible": rate(out["official"]["responses"]),
        "official_chat_visible": rate(out["official"]["chat"]),
        "custom_responses_visible": rate(out["custom"]["responses"]),
        "custom_chat_visible": rate(out["custom"]["chat"]),
        "import": out["import"].get("result") or out["import"].get("error"),
    }
    # never write credentials
    OUT.write_text(json.dumps(out, ensure_ascii=False, indent=2), encoding="utf-8")
    print("\n=== SUMMARY ===")
    print(json.dumps(out["summary"], ensure_ascii=False, indent=2))
    print("saved", OUT)


if __name__ == "__main__":
    main()
