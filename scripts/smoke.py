#!/usr/bin/env python3
"""End-to-end smoke test against a running API (standard library only).

    python3 scripts/smoke.py [http://localhost:8080]

It walks the same path a learner takes and asserts the invariants the product depends on. CI runs it
against a freshly started server with a throwaway database; locally it is a five-second check after a
backend change. Anything that fails here is a real regression, not a cosmetic difference.
"""

import json
import sys
import uuid
import urllib.error
import urllib.request

ROOT = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080").rstrip("/")
BASE = ROOT + "/v1"


def call(method, path, body=None, token=None):
    """Call the versioned API. Pass path="" for the root endpoints (/healthz, /readyz)."""
    data = json.dumps(body).encode() if body is not None else None
    base = ROOT if path == "" or path.startswith("/health") or path.startswith("/ready") else BASE
    request = urllib.request.Request(base + path, data=data, method=method)
    request.add_header("Content-Type", "application/json")
    if token:
        request.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(request) as response:
            raw = response.read()
            return response.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as error:
        raw = error.read()
        return error.code, json.loads(raw) if raw else {}


def expect(condition, message):
    if not condition:
        raise AssertionError(message)
    print(f"  ok  {message}")


def main():
    print("readiness")
    status, body = call("GET", "/healthz")
    expect(status == 200 and body["status"] == "ok", "liveness answers")
    status, body = call("GET", "/readyz")
    expect(status == 200 and body["status"] == "ok", "readiness answers")
    checked = {check["name"]: check["status"] for check in body["checks"]}
    expect(checked.get("database") == "ok", "the database probe is real and passing")
    expect(checked.get("migrations") == "ok", "the schema is up to date")

    print("taxonomy")
    status, body = call("GET", "/subjects")
    expect(status == 200 and body["subjects"], "subjects are served")
    subject_id = body["subjects"][0]["id"]

    print("signup draft survives across requests")
    status, draft = call("POST", "/signup-drafts", {"referralCode": "AMARA-2026"})
    expect(status == 201 and draft["token"], "a draft can be created")
    draft_token = draft["token"]
    status, _ = call("PATCH", "/signup-drafts/current", {"examPathwayKey": "waec"}, draft_token)
    expect(status == 200, "an answer can be written to the draft")
    status, resumed = call("GET", "/signup-drafts/current", None, draft_token)
    expect(status == 200 and resumed["draft"]["examPathwayKey"] == "waec", "progress is read back from the server")

    print("learner session")
    status, login = call("POST", "/auth/login", {"email": "student@edumart.ng", "password": "Student123!"})
    expect(status == 200 and login["token"], "demo learner can sign in")
    token = login["token"]

    print("practice attempt")
    status, started = call("POST", "/practice-sessions", {"subjectId": subject_id, "count": 3}, token)
    expect(status == 201, "practice starts")
    session = started["session"]
    expect(session["status"] == "in_progress", "the attempt is live")
    expect("answerOptionId" not in json.dumps(session), "answers are hidden before release")
    expect(session["remaining_seconds"] > 0, "the server owns the countdown")

    session_id = session["session_id"]
    question = session["questions"][0]
    qv_id, option_id = question["qvId"], question["options"][0]["id"]
    save_path = f"/assessment-sessions/{session_id}/responses/{qv_id}"

    status, saved = call("PUT", save_path, {"selectedOptionId": option_id, "expectedVersion": 0, "clientSequence": 1}, token)
    expect(status == 200 and saved["version"] == 1, "an answer saves at version 1")
    status, replay = call("PUT", save_path, {"selectedOptionId": option_id, "expectedVersion": 0, "clientSequence": 1}, token)
    expect(status == 200 and replay["version"] == 1, "a replayed save is idempotent")
    status, stale = call("PUT", save_path, {"selectedOptionId": "opt_4", "expectedVersion": 0, "clientSequence": 2}, token)
    expect(status == 409 and stale["error"]["details"]["current_version"] == 1, "a stale save is a 409 with the current version")

    status, submitted = call("POST", f"/assessment-sessions/{session_id}/submit", None, token)
    expect(status == 200, "the attempt submits")
    final = submitted["session"]
    expect(final["result_released"] is True, "results are released after submit")
    expect(len(final["review"]) == 3, "every question has a review entry")
    expect(final["review"][0]["answerOptionId"], "the answer key is available after release")

    print("corrections")
    correct = final["review"][0]["answerOptionId"]
    correction_path = f"/assessment-sessions/{session_id}/corrections/{qv_id}"
    status, corrected = call("PUT", correction_path, {"selectedOptionId": correct, "expectedVersion": 1}, token)
    expect(status == 200 and corrected["saved"], "a practice answer can be corrected after submit")
    expect("score" not in json.dumps(corrected), "a correction does not leak the score")

    print("sessions")
    status, sessions = call("GET", "/me/sessions", None, token)
    expect(status == 200 and sessions["sessions"], "the caller's sessions are listed")
    expect(sum(1 for s in sessions["sessions"] if s["current"]) == 1, "exactly one session is current")
    expect(all("token" not in s for s in sessions["sessions"]), "no session token is exposed")

    print("commerce")
    status, products = call("GET", "/products", None, token)
    expect(status == 200 and products["products"], "the store lists products")
    expect(all(isinstance(p["priceMinor"], int) for p in products["products"]), "prices are integer minor units")

    print("verification")
    verified_user = call("GET", "/me", None, token)[1]["user"]
    expect(verified_user["emailVerified"] is True, "a seeded account has a proven address")

    # A brand-new account is unverified, and the money path refuses it until it proves the address.
    # The code itself travels out of band, so this script asserts the gate rather than the code.
    fresh_email = "smoke-%s@example.com" % uuid.uuid4().hex[:10]
    status, registered = call(
        "POST",
        "/auth/register",
        {
            "displayName": "Smoke Learner",
            "email": fresh_email,
            "password": "SmokeTest123!",
            "dailyMinutes": 30,
            "client": "web",
        },
    )
    expect(status == 201, "a new account can be registered")
    fresh_token = registered["token"]
    expect(registered["user"]["emailVerified"] is False, "a new account starts unverified")

    status, attempt = call("POST", "/orders", {"productId": products["products"][0]["id"]}, fresh_token)
    expect(status == 403 and attempt["error"]["code"] == "EMAIL_NOT_VERIFIED",
           "buying is refused until the address is confirmed")

    status, issued = call("POST", "/auth/verification/request", {"channel": "email"}, fresh_token)
    expect(status == 202 and issued["sent"] is True, "a verification code is accepted for delivery")
    expect(issued["destination"] != fresh_email and "@example.com" in issued["destination"],
           "the destination is masked in the response")

    print("\nsmoke passed")


if __name__ == "__main__":
    main()
