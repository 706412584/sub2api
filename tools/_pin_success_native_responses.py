#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Pin previously-visible accounts to proxy 5, test native /v1/responses, restore."""
from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from pathlib import Path

BASE = "http://127.0.0.1:18080"
OUT = Path("/tmp/pin_success_native_responses.json")
MODEL = "grok-4.5"
PROMPT = "Solve step by step: what is 17*19? Show brief reasoning."
# Success accounts from earlier samples + historical 772
PIN_IDS = [1744, 1745, 772]
GROUP2_ONLY = [1744, 1745]  # used with group2 key for native responses


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


def safe(s, n=200):
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
        "event_top": dict(sorted(event_types.items(), key=lambda x: -x[1])[:10]),
    }


def parse_responses_json(raw: str) -> dict:
    try:
        j = json.loads(raw)
    except Exception:
        return {"visible": False, "err": "bad_json"}
    rchars = 0
    preview = ""
    for o in j.get("output") or []:
        if not isinstance(o, dict) or o.get("type") != "reasoning":
            continue
        s = o.get("summary")
        if isinstance(s, list):
            for p in s:
                if isinstance(p, dict):
                    t = p.get("text") or ""
                    rchars += len(t)
                    if not preview and t:
                        preview = t
        elif isinstance(s, str):
            rchars += len(s)
            preview = s
    usage = j.get("usage") or {}
    rtok = int((usage.get("output_tokens_details") or {}).get("reasoning_tokens") or 0)
    return {
        "visible": rchars > 0,
        "reasoning_chars": rchars,
        "reasoning_preview": safe(preview, 120),
        "reasoning_tokens": rtok,
        "usage": usage,
    }


def main():
    results = {
        "setup": [],
        "probes": [],
        "native_stream": [],
        "native_nonstream": [],
        "usage": [],
        "restore": [],
    }
    disabled = []  # (id, original_schedulable)
    proxy_restore = []  # (id, original_proxy)

    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    token = json.loads(raw)["data"]["access_token"]
    st, raw = req("GET", "/api/v1/admin/groups/2/api-keys", token=token)
    key2 = json.loads(raw)["data"]["items"][0]["key"]
    print("login ok")

    try:
        # snapshot + bind proxy 5 for pin accounts
        for aid in PIN_IDS:
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
            # ensure pin targets schedulable
            if orig_sched is False:
                st3, raw3 = set_schedulable(token, aid, True)
                row["force_sched_true"] = st3
            results["setup"].append(row)
            print(
                f"setup {aid} proxy {orig_proxy}->5 groups={acc.get('group_ids')} sched={orig_sched}"
            )

        # Disable other group2 schedulable accounts so LB sticks to 1744/1745
        st, raw = req(
            "GET",
            "/api/v1/admin/accounts?group_id=2&page_size=100&status=active",
            token=token,
        )
        items = json.loads(raw).get("data", {}).get("items") or []
        # paginate a bit more if needed
        page = 2
        while len(items) >= 100 and page <= 5:
            st, raw = req(
                "GET",
                f"/api/v1/admin/accounts?group_id=2&page_size=100&page={page}&status=active",
                token=token,
            )
            more = json.loads(raw).get("data", {}).get("items") or []
            if not more:
                break
            items.extend(more)
            page += 1

        pin_set = set(GROUP2_ONLY)
        for a in items:
            aid = a.get("id")
            if aid in pin_set:
                continue
            if a.get("schedulable") is False:
                continue
            st, raw = set_schedulable(token, aid, False)
            if st == 200:
                disabled.append((aid, True))
            else:
                results["setup"].append(
                    {"disable_fail": aid, "http": st, "err": safe(raw, 120)}
                )
        print(f"disabled other group2 accounts: {len(disabled)} (pin={GROUP2_ONLY})")
        results["setup"].append({"disabled_count": len(disabled), "pin": GROUP2_ONLY})

        # wait briefly for cache
        time.sleep(1.5)

        # Admin probes on each pin account via proxy 5
        print("\n=== PROBES proxy5 ===")
        for aid in PIN_IDS:
            t0 = time.time()
            st, raw = req(
                "POST",
                "/api/v1/admin/proxies/5/grok-reasoning-probe",
                {"account_id": aid, "confirm_quota_cost": True},
                token=token,
                timeout=120,
            )
            dt = round(time.time() - t0, 2)
            row = {"account_id": aid, "http": st, "dt": dt}
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
                        "model",
                        "latency_ms",
                    ]:
                        if k in data:
                            row[k] = data[k]
            else:
                row["err"] = safe(raw)
            results["probes"].append(row)
            print(
                f"probe {aid} http={st} status={row.get('status')} "
                f"vis={row.get('has_visible_reasoning')} chars={row.get('visible_reasoning_chars')} "
                f"rtok={row.get('reasoning_tokens')} msg={safe(str(row.get('message') or row.get('err') or ''), 80)}"
            )
            time.sleep(0.8)

        # Native /v1/responses stream x4 (should land on 1744/1745 only)
        print("\n=== NATIVE /v1/responses STREAM ===")
        for i in range(4):
            payload = {
                "model": MODEL,
                "input": PROMPT,
                "reasoning": {"effort": "high"},
                "stream": True,
            }
            t0 = time.time()
            st, raw = req(
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
                row["err"] = safe(raw)
            results["native_stream"].append(row)
            print(
                f"S{i+1} http={st} vis={row.get('visible')} rchars={row.get('reasoning_chars')} "
                f"rtok={row.get('reasoning_tokens')} dt={dt} err={row.get('err')}"
            )
            time.sleep(1.2)

        print("\n=== NATIVE /v1/responses NONSTREAM ===")
        for i in range(3):
            payload = {
                "model": MODEL,
                "input": PROMPT,
                "reasoning": {"effort": "high"},
                "stream": False,
            }
            t0 = time.time()
            st, raw = req(
                "POST", "/v1/responses", data=payload, apikey=key2, timeout=120
            )
            dt = round(time.time() - t0, 2)
            row = {"i": i + 1, "http": st, "dt": dt, "bytes": len(raw)}
            if st == 200:
                row.update(parse_responses_json(raw))
            else:
                row["err"] = safe(raw)
            results["native_nonstream"].append(row)
            print(
                f"N{i+1} http={st} vis={row.get('visible')} rchars={row.get('reasoning_chars')} "
                f"rtok={row.get('reasoning_tokens')} preview={row.get('reasoning_preview')} dt={dt}"
            )
            time.sleep(1.2)

        # usage attribution
        st, raw = req("GET", "/api/v1/admin/usage?page_size=15", token=token)
        if st == 200:
            for u in json.loads(raw).get("data", {}).get("items") or []:
                if u.get("api_key_id") == 2 or u.get("group_id") == 2:
                    results["usage"].append(
                        {
                            "id": u.get("id"),
                            "account_id": u.get("account_id"),
                            "inbound": u.get("inbound_endpoint"),
                            "upstream": u.get("upstream_endpoint"),
                            "stream": u.get("stream"),
                            "reasoning_tokens": u.get("reasoning_tokens"),
                            "model": u.get("model"),
                        }
                    )
        print("\nusage sample:")
        for u in results["usage"][:10]:
            print(u)

    finally:
        print("\n=== RESTORE ===")
        # re-enable disabled accounts
        for aid, _ in disabled:
            st, raw = set_schedulable(token, aid, True)
            results["restore"].append({"reenable": aid, "http": st})
            if st != 200:
                print("reenable fail", aid, st, safe(raw))
        print(f"reenabled {len(disabled)}")
        # restore proxies
        for aid, orig in proxy_restore:
            if orig is None:
                continue
            # get current
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

    # summary
    def rate(rows):
        ok = [r for r in rows if r.get("http") == 200]
        if not ok:
            return "0/0"
        vis = sum(1 for r in ok if r.get("visible") or r.get("has_visible_reasoning"))
        return f"{vis}/{len(ok)}"

    summary = {
        "probe_visible": rate(results["probes"]),
        "stream_visible": rate(results["native_stream"]),
        "nonstream_visible": rate(results["native_nonstream"]),
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
