#!/usr/bin/env python3
"""Check every clearnet instance in instances.json.

Fetches /api/instance from each and prints one line per instance. Exits 1 if
any is down, so a scheduled CI job turns red and INSTANCES.md does not keep
listing dead links. Tor, I2P and Yggdrasil addresses are skipped: the runner
cannot reach them.
"""

import json
import sys
import urllib.error
import urllib.request

TIMEOUT = 15


def check(url):
    req = urllib.request.Request(
        url.rstrip("/") + "/api/instance",
        headers={"User-Agent": "skunkyart-instance-check"},
    )
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT) as resp:
            info = json.load(resp)
    except (urllib.error.URLError, json.JSONDecodeError, TimeoutError) as err:
        return None, str(err)
    return info.get("version", "?"), None


def main():
    with open("instances.json", encoding="utf-8") as f:
        instances = json.load(f)["instances"]

    failed = 0
    for inst in instances:
        url = inst.get("urls", {}).get("clearnet")
        if not url:
            print(f"{inst['title']}: no clearnet url, skipped")
            continue
        version, err = check(url)
        if err:
            failed += 1
            print(f"{inst['title']}: DOWN {url} ({err})")
        else:
            print(f"{inst['title']}: ok {url} v{version}")

    if failed:
        print(f"{failed} instance(s) down")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
