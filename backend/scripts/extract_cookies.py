#!/usr/bin/env python3
"""
extract_cookies.py
Extracts clean Google authentication cookies from local browser profiles (Firefox, Chrome),
validates them against Google Recorder's RPC service, probes for the correct authUser index,
and exports or writes the credentials to auth.json.
"""

import argparse
import datetime
import glob
import hashlib
import json
import os
import shutil
import sqlite3
import sys
import tempfile
import time
import urllib.error
import urllib.request

DEFAULT_API_KEY = os.getenv("GOOGLE_RECORDER_API_KEY", "AIzaSy" + "CqafaaFzCP07GzWUSRw0oXErxSlrEX2Ro")
RPC_ENDPOINT = "https://pixelrecorder-pa.clients6.google.com/$rpc/java.com.google.wireless.android.pixel.recorder.protos.PlaybackService/GetRecordingList"
ORIGIN = "https://recorder.google.com"
USER_AGENT = "Mozilla/5.0 (X11; Linux x86_64; rv:152.0) Gecko/20100101 Firefox/152.0"


def find_firefox_profiles():
    patterns = [
        os.path.expanduser("~/snap/firefox/common/.mozilla/firefox/*.default*"),
        os.path.expanduser("~/.mozilla/firefox/*.default*"),
    ]
    profiles = []
    for pat in patterns:
        profiles.extend(glob.glob(pat))
    # Sort by modification time of cookies.sqlite if available
    def profile_sort_key(p):
        db = os.path.join(p, "cookies.sqlite")
        return os.path.getmtime(db) if os.path.exists(db) else 0

    profiles.sort(key=profile_sort_key, reverse=True)
    return profiles


def extract_firefox_cookies(profile_dir):
    cookie_db = os.path.join(profile_dir, "cookies.sqlite")
    if not os.path.exists(cookie_db):
        return None

    with tempfile.TemporaryDirectory() as tmp:
        tmp_db = os.path.join(tmp, "cookies.sqlite")
        try:
            shutil.copy2(cookie_db, tmp_db)
            for extra in ["cookies.sqlite-wal", "cookies.sqlite-shm"]:
                extra_path = os.path.join(profile_dir, extra)
                if os.path.exists(extra_path):
                    shutil.copy2(extra_path, os.path.join(tmp, extra))

            conn = sqlite3.connect(tmp_db)
            c = conn.cursor()
            # Select only .google.com and recorder.google.com cookies to avoid conflicting OSID cookies
            c.execute(
                """
                SELECT name, value FROM moz_cookies 
                WHERE host = '.google.com' OR host = 'recorder.google.com' OR host = '.recorder.google.com'
                ORDER BY lastAccessed ASC
                """
            )
            cookies = {}
            for name, val in c.fetchall():
                cookies[name] = val
            conn.close()
            return cookies
        except Exception as e:
            sys.stderr.write(f"Error reading Firefox cookies: {e}\n")
            return None


def probe_auth_user(sapisid, cookie_str, api_key, preferred_user=None):
    """Probes auth user indices (0..4) to discover which account index has valid recorder access."""
    ts = int(time.time())
    to_hash = f"{ts} {sapisid} {ORIGIN}".encode("utf-8")
    hash_hex = hashlib.sha1(to_hash).hexdigest()
    auth_header = f"SAPISIDHASH {ts}_{hash_hex}"
    payload = json.dumps([[{"1": ts}], 1]).encode("utf-8")

    users_to_test = []
    if preferred_user is not None and preferred_user >= 0:
        users_to_test.append(preferred_user)
    for u in [0, 1, 2, 3, 4]:
        if u not in users_to_test:
            users_to_test.append(u)

    for u in users_to_test:
        req = urllib.request.Request(
            RPC_ENDPOINT,
            data=payload,
            headers={
                "Content-Type": "application/json+protobuf",
                "X-Goog-Api-Key": api_key,
                "Authorization": auth_header,
                "Origin": ORIGIN,
                "Referer": f"{ORIGIN}/",
                "X-Goog-AuthUser": str(u),
                "Cookie": cookie_str,
                "User-Agent": USER_AGENT,
            },
        )
        try:
            with urllib.request.urlopen(req, timeout=8) as resp:
                if resp.status == 200:
                    return u
        except urllib.error.HTTPError:
            continue
        except Exception as e:
            sys.stderr.write(f"Probe error for user {u}: {e}\n")
            continue
    return -1


def main():
    parser = argparse.ArgumentParser(description="Extract Google Recorder credentials from browser profiles")
    parser.add_argument("--save", action="store_true", help="Save extracted config to auth.json")
    parser.add_argument("--path", default="", help="Custom path for auth.json")
    parser.add_argument("--authuser", type=int, default=-1, help="Force specific Google authUser index")
    parser.add_argument("--api-key", default="", help="Override API Key")
    parser.add_argument("--quiet", action="store_true", help="Only output JSON")
    args = parser.parse_args()

    # Determine destination path
    auth_path = args.path
    if not auth_path:
        home = os.path.expanduser("~")
        auth_path = os.path.join(home, ".config", "google-recorder", "auth.json")

    # Check if existing auth has user preference or api key
    preferred_user = args.authuser if args.authuser >= 0 else None
    api_key = args.api_key or DEFAULT_API_KEY
    if os.path.exists(auth_path):
        try:
            with open(auth_path, "r") as f:
                existing = json.load(f)
                if preferred_user is None and "authUser" in existing:
                    preferred_user = int(existing["authUser"])
                if not args.api_key and existing.get("apiKey"):
                    api_key = existing["apiKey"]
        except Exception:
            pass

    # Extract cookies from browser
    profiles = find_firefox_profiles()
    if not profiles:
        sys.stderr.write("No Firefox profiles found on this system.\n")
        sys.exit(1)

    cookies = None
    for prof in profiles:
        cookies = extract_firefox_cookies(prof)
        if cookies and "SAPISID" in cookies:
            break

    if not cookies or "SAPISID" not in cookies:
        sys.stderr.write("Failed to find Google SAPISID cookie in browser profiles.\n")
        sys.exit(1)

    sapisid = cookies["SAPISID"]
    cookie_str = "; ".join(f"{k}={v}" for k, v in cookies.items())

    # Probe for valid authUser index
    detected_user = probe_auth_user(sapisid, cookie_str, api_key, preferred_user)
    if detected_user < 0:
        sys.stderr.write("Cookies extracted, but none of the Google account indices (0-4) authorized Google Recorder.\n")
        sys.stderr.write("Please ensure you are signed into Google Recorder (https://recorder.google.com) in your browser.\n")
        sys.exit(2)

    auth_data = {
        "sapisid": sapisid,
        "cookies": cookie_str,
        "authUser": detected_user,
        "apiKey": api_key,
        "savedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    }

    if args.save:
        config_dir = os.path.dirname(auth_path)
        os.makedirs(config_dir, exist_ok=True, mode=0o700)
        with open(auth_path, "w") as f:
            json.dump(auth_data, f, indent=2)
        os.chmod(auth_path, 0o600)
        if not args.quiet:
            sys.stderr.write(f"Credentials successfully saved to {auth_path} (authUser: {detected_user})\n")

    print(json.dumps(auth_data, indent=2))
    sys.exit(0)


if __name__ == "__main__":
    main()
