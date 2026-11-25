#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Delete grok801 accounts imported at 2026-08-03 02:03:30/31 (ids from list)."""
from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from collections import Counter
from pathlib import Path

BASE = "http://127.0.0.1:18080"
IDS_PATH = Path("/tmp/grok801_remove_ids.json")
OUT = Path("/tmp/grok801_remove_result.json")
LOG = Path("/tmp/grok801_remove.log")
GROUP_ID = 11
CREATED_PREFIX = "2026-08-03T02:03:3"  # 02:03:30 or 02:03:31


def log(msg: str) -> None:
    line = msg if msg.endswith("\n") else msg + "\n"
    print(msg, flush=True)
    with LOG.open("a", encoding="utf-8") as f:
        f.write(line)


def req(method, path, data=None, token=None, timeout=60):
    h = {"Content-Type": "application/json"}
    if token:
        h["Authorization"] = f"Bearer {token}"
    body = None if data is None else json.dumps(data, ensure_ascii=False).encode()
    r = urllib.request.Request(BASE + path, data=body, headers=h, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:
        return 0, str(e)


def main():
    LOG.write_text("", encoding="utf-8")
    st, raw = req(
        "POST",
        "/api/v1/auth/login",
        {"email": "admin@local.test", "password": "12345678"},
    )
    if st != 200:
        log(f"login_fail {st} {raw[:300]}")
        raise SystemExit(1)
    token = json.loads(raw)["data"]["access_token"]
    log("login ok")

    payload = json.loads(IDS_PATH.read_text(encoding="utf-8"))
    ids = list(payload["ids"])
    log(f"list count={len(ids)} group={payload.get('group_id')} prefix={payload.get('created_prefix')}")

    # Pre-check group
    st, raw = req("GET", f"/api/v1/admin/groups/{GROUP_ID}", token=token)
    if st != 200:
        log(f"group_get_fail {st} {raw[:300]}")
        raise SystemExit(1)
    g = json.loads(raw)["data"]
    before_count = g.get("account_count")
    log(f"BEFORE group11 name={g.get('name')} account_count={before_count}")

    # Re-verify each id still matches batch criteria before delete
    verified = []
    skip = []
    for i, aid in enumerate(ids, 1):
        st, raw = req("GET", f"/api/v1/admin/accounts/{aid}", token=token)
        if st == 404:
            skip.append({"id": aid, "reason": "already_gone", "http": st})
            continue
        if st != 200:
            skip.append({"id": aid, "reason": f"get_fail_{st}", "http": st, "body": raw[:200]})
            continue
        acc = json.loads(raw)["data"]
        created = str(acc.get("created_at") or "")
        gids = acc.get("group_ids") or []
        ok_time = created.startswith("2026-08-03T02:03:30") or created.startswith(
            "2026-08-03T02:03:31"
        )
        ok_group = GROUP_ID in gids
        if not (ok_time and ok_group):
            skip.append(
                {
                    "id": aid,
                    "reason": "criteria_mismatch",
                    "created_at": created,
                    "group_ids": gids,
                }
            )
            continue
        verified.append(aid)
        if i % 50 == 0 or i == len(ids):
            log(f"verify progress {i}/{len(ids)} verified={len(verified)} skip={len(skip)}")

    log(f"verified_for_delete={len(verified)} skip={len(skip)}")

    deleted = []
    failed = []
    for i, aid in enumerate(verified, 1):
        st, raw = req("DELETE", f"/api/v1/admin/accounts/{aid}", token=token, timeout=30)
        if st in (200, 204):
            deleted.append(aid)
        else:
            failed.append({"id": aid, "http": st, "body": raw[:300]})
        if i % 25 == 0 or i == len(verified):
            log(f"delete progress {i}/{len(verified)} ok={len(deleted)} fail={len(failed)}")
        # tiny pause to avoid hammering
        if i % 20 == 0:
            time.sleep(0.05)

    # Post-check: sample first/mid/last of intended deletes
    sample_ids = []
    if verified:
        sample_ids = [verified[0], verified[len(verified) // 2], verified[-1]]
    sample_ids = list(dict.fromkeys(sample_ids + ids[:3] + ids[-3:]))
    still_present = []
    gone = []
    for aid in sample_ids:
        st, raw = req("GET", f"/api/v1/admin/accounts/{aid}", token=token)
        if st == 404:
            gone.append(aid)
        elif st == 200:
            still_present.append({"id": aid, "body_snip": raw[:120]})
        else:
            still_present.append({"id": aid, "http": st, "body": raw[:120]})

    st, raw = req("GET", f"/api/v1/admin/groups/{GROUP_ID}", token=token)
    after = json.loads(raw)["data"] if st == 200 else {}
    after_count = after.get("account_count")
    log(
        f"AFTER group11 name={after.get('name')} account_count={after_count} "
        f"delta={None if before_count is None or after_count is None else before_count - after_count}"
    )

    # Extra: count how many of original ids still exist via GetAccountsByIDs-style individual gets
    remain = 0
    remain_ids = []
    for i, aid in enumerate(ids, 1):
        st, raw = req("GET", f"/api/v1/admin/accounts/{aid}", token=token)
        if st == 200:
            remain += 1
            remain_ids.append(aid)
        if i % 100 == 0:
            log(f"remain-scan {i}/{len(ids)} remain={remain}")

    result = {
        "before_account_count": before_count,
        "after_account_count": after_count,
        "list_count": len(ids),
        "verified_count": len(verified),
        "deleted_count": len(deleted),
        "failed_count": len(failed),
        "skip_count": len(skip),
        "remain_of_list": remain,
        "remain_ids_sample": remain_ids[:20],
        "sample_gone": gone,
        "sample_still_present": still_present,
        "failed": failed[:50],
        "skip": skip[:50],
        "deleted_first_last": [deleted[0], deleted[-1]] if deleted else [],
    }
    OUT.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    log("SUMMARY " + json.dumps({k: result[k] for k in result if k not in ("failed", "skip")}, ensure_ascii=False))
    log(f"wrote {OUT}")


if __name__ == "__main__":
    main()
