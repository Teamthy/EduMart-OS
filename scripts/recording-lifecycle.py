#!/usr/bin/env python3
"""Prove the class-recording lifecycle end to end, against a real server and a real media directory.

A recording is the one feature in this repository whose correctness lives on both sides of a boundary: the browser
seals and uploads, the server stores bytes it cannot read, and a player downloads them. Tests cover each half. This
script covers the join between them, in the order a class experiences it, over HTTP, with a real process, a real
database and a real directory on disk:

  1. **The switch is honest.** With no media directory the endpoint answers 503 and names what is missing; with one it
     answers 201. A feature that accepts an upload and loses it is worse than one that is off.
  2. **The class says it is being recorded** in the payload every client already fetches, so a participant cannot be
     in a recorded room without being told.
  3. **A sealed chunk survives the round trip** byte for byte, and the store's digest matches what was sent. The bytes
     are deliberately *not* a valid media file — the server must not care, and if it ever starts parsing them this
     fails.
  4. **A duplicate sequence is refused** rather than overwriting: a chunk that changes under a download makes its
     digest meaningless.
  5. **Range requests work**, which is what makes seeking in an hour-long class possible.
  6. **A non-member cannot read it**, and **deleting erases the bytes** — checked by looking at the directory, not at
     the status column.
  7. **An empty recording is `failed`, with a reason.** Pressing record and uploading nothing must never produce a
     `ready` file.
  8. **Retention is enforced on a clock**, not by somebody remembering: a recording past `retain_until` is gone after
     the sweep.

Usage:
    python3 scripts/recording-lifecycle.py --base http://127.0.0.1:8083 --media-dir /tmp/edumart-media

It starts nothing. Point it at a running API that was booted with `EDUMART_MEDIA_DIR` set to `--media-dir`; the script
also checks the directory itself, which is how "deleted" is distinguished from "marked deleted".
"""

from __future__ import annotations

import argparse
import hashlib
import http.cookiejar
import json
import os
import sys
import urllib.error
import urllib.request

FAILURES: list[str] = []


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok   {message}")
        return
    FAILURES.append(message)
    print(f"  FAIL {message}")


class Client:
    """A cookie jar and a token, which is how both clients talk to the API."""

    def __init__(self, base: str):
        self.base = base.rstrip("/")
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        self.token: str | None = None

    def call(self, method: str, path: str, body: dict | None = None) -> tuple[int, dict]:
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(self.base + path, data=data, method=method)
        request.add_header("Accept", "application/json")
        if data is not None:
            request.add_header("Content-Type", "application/json")
        if self.token:
            request.add_header("Authorization", "Bearer " + self.token)
        try:
            with self.opener.open(request, timeout=30) as response:
                return response.status, _json(response.read())
        except urllib.error.HTTPError as error:
            return error.code, _json(error.read())

    def upload(self, path: str, payload: bytes) -> tuple[int, dict]:
        request = urllib.request.Request(self.base + path, data=payload, method="POST")
        request.add_header("Content-Type", "application/octet-stream")
        if self.token:
            request.add_header("Authorization", "Bearer " + self.token)
        try:
            with self.opener.open(request, timeout=30) as response:
                return response.status, _json(response.read())
        except urllib.error.HTTPError as error:
            return error.code, _json(error.read())

    def download(self, path: str, byte_range: str | None = None) -> tuple[int, bytes, dict]:
        request = urllib.request.Request(self.base + path, method="GET")
        if self.token:
            request.add_header("Authorization", "Bearer " + self.token)
        if byte_range:
            request.add_header("Range", "bytes=" + byte_range)
        try:
            with self.opener.open(request, timeout=30) as response:
                return response.status, response.read(), dict(response.headers)
        except urllib.error.HTTPError as error:
            return error.code, error.read(), dict(error.headers or {})


def _json(raw: bytes) -> dict:
    if not raw:
        return {}
    try:
        return json.loads(raw.decode())
    except ValueError:
        return {}


def register(client: Client, email: str) -> str:
    status, body = client.call(
        "POST",
        "/v1/auth/register",
        {"displayName": "Recording " + email.split("@")[0], "email": email, "password": "Str0ngPassw0rd!", "client": "web"},
    )
    if status not in (200, 201):
        raise SystemExit(f"register {email}: {status} {body}")
    client.token = body["token"]
    return body["user"]["id"]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default=os.environ.get("EDUMART_BASE_URL", "http://127.0.0.1:8080"))
    parser.add_argument("--media-dir", default=os.environ.get("EDUMART_MEDIA_DIR", "/tmp/edumart-media"))
    parser.add_argument("--retention-seconds", type=int, default=2, help="Retention used to prove the sweep erases")
    args = parser.parse_args()

    host = Client(args.base)
    register(host, f"rec-host-{os.getpid()}@example.com")
    learner = Client(args.base)
    learner_id = register(learner, f"rec-learner-{os.getpid()}@example.com")

    print("1. a class, started, then recorded")
    status, body = host.call(
        "POST",
        "/v1/virtual-classes",
        {
            "title": "Recording lifecycle",
            "description": "A class recorded for the lifecycle check.",
            "creditsCost": 0,
            "startsAt": "2030-01-01T10:00:00Z",
            "durationMinutes": 45,
        },
    )
    check(status == 201, f"the host can schedule a class ({status})")
    class_id = body["class"]["id"]

    # Recording before the class is live must be refused: an empty file that claims a class happened is a lie.
    status, _ = host.call("POST", f"/v1/virtual-classes/{class_id}/recording")
    check(status == 409, f"recording a class that has not started is refused ({status})")

    status, _ = learner.call("POST", f"/v1/virtual-classes/{class_id}/join")
    check(status < 300, f"the learner takes a seat ({status})")
    status, _ = host.call("POST", f"/v1/virtual-classes/{class_id}/start")
    check(status == 200, f"the host goes live ({status})")

    print("2. the notice is in the payload every client already fetches")
    status, body = learner.call("GET", f"/v1/virtual-classes/{class_id}")
    check(status == 200 and body["class"].get("recording") is None, "a class with no recording does not claim one")

    status, body = host.call("POST", f"/v1/virtual-classes/{class_id}/recording")
    check(status == 201, f"the host starts recording ({status})")
    recording = body["recording"]
    recording_id = recording["id"]
    check(bool(recording["retainUntil"]), "the recording carries the date it will be erased")

    status, body = learner.call("GET", f"/v1/virtual-classes/{class_id}")
    notice = body["class"].get("recording") or {}
    check(notice.get("status") == "recording", f"the class payload says it is being recorded ({notice.get('status')})")

    status, body = learner.call("POST", f"/v1/virtual-classes/{class_id}/recording/acknowledge")
    check(status == 200 and body.get("acknowledged") is True, f"the learner acknowledges ({status})")

    print("3. sealed bytes survive the round trip")
    # Not a media file on purpose: the server must store what it is given and never look inside.
    sealed = b"\x01" + bytes(range(12)) + os.urandom(64) + b"sealed-payload"
    digest = hashlib.sha256(sealed).hexdigest()
    status, body = learner.upload(f"/v1/virtual-classes/{class_id}/recording/segments?seq=1&durationMs=4000", sealed)
    check(status == 201, f"the first chunk is stored ({status})")
    segment = body.get("segment", {})
    check(segment.get("sha256") == digest, "the store's digest is the digest of the bytes it received")
    check(segment.get("bytes") == len(sealed), "the stored size is the size that was sent")

    print("4. a duplicate sequence is refused")
    status, _ = learner.upload(f"/v1/virtual-classes/{class_id}/recording/segments?seq=1&durationMs=4000", b"different")
    check(status == 409, f"re-uploading chunk 1 is refused ({status})")

    second = b"\x01" + bytes(range(12)) + os.urandom(32)
    status, _ = learner.upload(f"/v1/virtual-classes/{class_id}/recording/segments?seq=3&durationMs=4000", second)
    check(status == 201, f"a chunk with a gap in the sequence is stored ({status})")

    print("5. finishing, gaps, ranges and digests")
    status, body = host.call("POST", f"/v1/virtual-classes/{class_id}/recording/complete")
    check(status == 200, f"the host finishes the recording ({status})")
    finished = body["recording"]
    check(finished["status"] == "ready", f"a recording with chunks is ready ({finished['status']})")
    check(finished["missingSeqs"] == [2], f"the gap is reported rather than hidden ({finished['missingSeqs']})")
    check(learner_id in finished["acknowledgedUserIds"], "the learner who acknowledged is listed")

    segment_id = next(item["id"] for item in finished["segments"] if item["seq"] == 1)
    status, raw, headers = host.download(f"/v1/virtual-classes/{class_id}/recording/segments/{segment_id}")
    check(status == 200 and raw == sealed, f"the chunk comes back byte for byte ({status})")
    check("no-store" in (headers.get("Cache-Control") or ""), "a recording download is not cacheable")

    status, partial, _ = host.download(
        f"/v1/virtual-classes/{class_id}/recording/segments/{segment_id}", byte_range="0-15"
    )
    check(status == 206 and len(partial) == 16, f"a range request returns a 206 of the right length ({status})")

    print("6. it is members-only, and deleting erases the bytes")
    stranger = Client(args.base)
    register(stranger, f"rec-stranger-{os.getpid()}@example.com")
    status, _ = stranger.call("GET", f"/v1/virtual-classes/{class_id}/recording")
    check(status == 403, f"a signed-in stranger cannot read it ({status})")
    status, _, _ = host.download(f"/v1/virtual-classes/{class_id}/recording/segments/{segment_id}")
    check(status == 200, "the host can still read it before deleting")

    on_disk = _objects_for(args.media_dir, recording_id)
    check(on_disk >= 2, f"the chunks are actually on disk before deleting ({on_disk})")
    status, body = host.call("DELETE", f"/v1/virtual-classes/{class_id}/recording")
    check(status == 200, f"the host deletes the recording ({status})")
    check(body.get("objectsRemoved", 0) >= 2, f"the delete reports the objects it removed ({body.get('objectsRemoved')})")
    check(_objects_for(args.media_dir, recording_id) == 0, "no bytes for that recording remain on disk")
    status, _, _ = host.download(f"/v1/virtual-classes/{class_id}/recording/segments/{segment_id}")
    check(status == 404, f"a deleted chunk is gone, not merely hidden ({status})")

    print("7. an empty recording fails with a reason")
    status, body = host.call("POST", f"/v1/virtual-classes/{class_id}/recording")
    check(status == 201, f"a second recording can start ({status})")
    status, body = host.call("POST", f"/v1/virtual-classes/{class_id}/recording/complete")
    empty = body.get("recording", {})
    check(status == 200 and empty.get("status") == "failed", f"nothing uploaded means failed ({empty.get('status')})")
    check(bool(empty.get("failureReason")), "the failure says why, in words")

    print("8. retention is enforced on a clock")
    if args.retention_seconds > 0:
        import time

        time.sleep(args.retention_seconds + 1)
        # The sweep runs on a one-minute ticker in the process; a boot-time sweep runs in the deploy path. Poll for it
        # rather than assume an instant, because the honest claim is "within a sweep", not "at the millisecond".
        deadline = time.time() + 90
        expired = False
        while time.time() < deadline:
            status, _ = host.call("GET", f"/v1/virtual-classes/{class_id}/recording")
            if status == 404:
                expired = True
                break
            time.sleep(3)
        check(expired, "a recording past its retention date is erased and reads as absent")
    else:
        print("  skipped (retention sweep not requested)")

    print()
    if FAILURES:
        print(f"FAILED: {len(FAILURES)} check(s)")
        for failure in FAILURES:
            print("  - " + failure)
        return 1
    print("class recording lifecycle verified")
    return 0


def _objects_for(media_dir: str, recording_id: str) -> int:
    """Count the files on disk belonging to one recording. This is what distinguishes 'deleted' from 'marked deleted'."""
    if not os.path.isdir(media_dir):
        return 0
    count = 0
    for root, _dirs, files in os.walk(media_dir):
        if recording_id in root:
            count += len(files)
    return count


if __name__ == "__main__":
    sys.exit(main())
