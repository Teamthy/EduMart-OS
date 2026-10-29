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
from datetime import datetime, timedelta, timezone

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

    print("token rotation")
    status, signed_in = call("POST", "/auth/login", {"email": "teacher@edumart.ng", "password": "Teacher123!"})
    expect(status == 200 and signed_in.get("refresh_token"), "sign-in returns an access and a refresh token")
    access, refresh = signed_in["token"], signed_in["refresh_token"]
    expect(access != refresh, "the two tokens are different credentials")
    expect(call("GET", "/me", None, refresh)[0] == 401,
           "a refresh token cannot authenticate ordinary requests")

    status, rotated = call("POST", "/auth/refresh", {"refreshToken": refresh})
    expect(status == 200 and rotated["refresh_token"] != refresh, "a refresh token rotates into a new pair")
    new_access, new_refresh = rotated["token"], rotated["refresh_token"]
    expect(call("GET", "/me", None, new_access)[0] == 200, "the rotated access token works")
    expect(call("GET", "/me", None, access)[0] == 401,
           "the superseded access token stops working")

    status, reused = call("POST", "/auth/refresh", {"refreshToken": refresh})
    expect(status == 401, "replaying a rotated refresh token is refused")
    if reused["error"]["code"] == "SESSION_REUSED":
        expect(call("POST", "/auth/refresh", {"refreshToken": new_refresh})[0] == 401,
               "genuine reuse revokes the whole family, not just the replayed token")
        expect(call("GET", "/me", None, new_access)[0] == 401,
               "the family's access token dies with it")
    else:
        expect(call("GET", "/me", None, new_access)[0] == 200,
               "a duplicate inside the grace window leaves the successor usable")

    status, second = call("POST", "/auth/login", {"email": "teacher@edumart.ng", "password": "Teacher123!"})
    expect(status == 200 and second.get("refresh_token"), "signing in again after a revocation works")
    expect(call("POST", "/auth/refresh", {"refreshToken": "not-a-real-token"})[0] == 401,
           "an unknown refresh token is refused")

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

    print("institutions")
    teacher_token = call("POST", "/auth/login", {"email": "teacher@edumart.ng", "password": "Teacher123!"})[1]["token"]

    status, created_org = call(
        "POST",
        "/organizations",
        {"name": "Smoke Test College %s" % uuid.uuid4().hex[:6], "kind": "school"},
        teacher_token,
    )
    expect(status == 201, "a verified account can register an organisation")
    organization = created_org["organization"]
    expect(organization["status"] == "pending_verification", "a new organisation starts unverified")
    expect(organization["role"] == "owner", "the creator owns the organisation they created")

    # Membership, not a chosen role, decides access: a student is not a member of a brand-new org.
    outsider_token = call("POST", "/auth/login", {"email": "student@edumart.ng", "password": "Student123!"})[1]["token"]
    status, body = call("GET", "/organizations/" + organization["id"], None, outsider_token)
    expect(status == 404, "a non-member cannot see an organisation, and gets 404 rather than 403")

    status, invited = call(
        "POST",
        "/organizations/" + organization["id"] + "/invitations",
        {"email": "student@edumart.ng", "role": "student"},
        teacher_token,
    )
    expect(status == 201 and invited["token"], "an owner can invite someone, and the token is returned once")

    status, joined = call("POST", "/organizations/invitations/accept", {"token": invited["token"]}, outsider_token)
    expect(status == 200 and joined["joined"] is True, "the invited account can join with the token")

    status, body = call("GET", "/organizations/" + organization["id"] + "/members", None, outsider_token)
    expect(status == 403, "a student member cannot read the organisation roster")

    status, body = call("POST", "/organizations/invitations/accept", {"token": invited["token"]}, outsider_token)
    expect(status >= 400, "an invitation cannot be used twice")

    status, body = call("GET", "/organizations/" + organization["id"], None, outsider_token)
    expect(status == 200, "the member can see the organisation after joining")

    student_id = call("GET", "/me", None, outsider_token)[1]["user"]["id"]
    status, removed = call(
        "DELETE", "/organizations/" + organization["id"] + "/members/" + student_id, None, teacher_token
    )
    expect(status == 200 and removed["removed"] is True, "an owner can remove a member")

    status, body = call("GET", "/organizations/" + organization["id"], None, outsider_token)
    expect(status == 404, "removing the membership removes the access it carried")

    print("institutions")
    teacher_token = call("POST", "/auth/login", {"email": "teacher@edumart.ng", "password": "Teacher123!"})[1]["token"]
    status, created = call("POST", "/organizations", {"name": "Smoke Academy " + uuid.uuid4().hex[:6], "kind": "school"}, teacher_token)
    expect(status == 201, "a verified teacher can register an institution")
    institution = created["organization"]
    expect(institution["status"] == "pending_verification" and not institution["verified"],
           "a new institution starts unverified")
    expect(institution["role"] == "owner", "the creator owns the institution")

    status, _ = call("POST", f"/admin/organizations/{institution['id']}/verify", None, teacher_token)
    expect(status == 403, "a teacher cannot approve their own institution")
    admin_token = call("POST", "/auth/login", {"email": "admin@edumart.ng", "password": "Admin123!"})[1]["token"]
    status, approved = call("POST", f"/admin/organizations/{institution['id']}/verify", None, admin_token)
    expect(status == 200 and approved["organization"]["verified"], "a platform administrator approves it")

    status, invited = call("POST", f"/organizations/{institution['id']}/invitations",
                           {"email": "student@edumart.ng", "role": "student"}, teacher_token)
    expect(status == 201 and invited["token"], "an owner can invite a member")
    student_token = call("POST", "/auth/login", {"email": "student@edumart.ng", "password": "Student123!"})[1]["token"]
    status, _ = call("POST", "/organizations/invitations/accept", {"token": invited["token"]}, student_token)
    expect(status == 200, "the invited account can accept")
    status, _ = call("POST", "/organizations/invitations/accept", {"token": invited["token"]}, student_token)
    expect(status == 409, "an invitation cannot be used twice")

    status, roster = call("GET", f"/organizations/{institution['id']}/members", None, teacher_token)
    expect(status == 200 and len(roster["members"]) == 2, "the roster shows both members")
    status, _ = call("GET", f"/organizations/{institution['id']}/members", None, student_token)
    expect(status == 403, "a student member cannot read the roster")
    status, _ = call("POST", f"/organizations/{institution['id']}/invitations",
                     {"email": "spy@example.com", "role": "teacher"}, student_token)
    expect(status == 403, "a student member cannot invite anyone")

    student_id = next(m["userId"] for m in roster["members"] if m["email"] == "student@edumart.ng")
    status, _ = call("DELETE", f"/organizations/{institution['id']}/members/{student_id}", None, teacher_token)
    expect(status == 200, "an owner can remove a member")
    status, _ = call("GET", f"/organizations/{institution['id']}", None, student_token)
    expect(status == 404, "removing the membership removes the access it carried")

    print("reporting")
    status, dashboard = call("GET", f"/organizations/{institution['id']}/report", None, teacher_token)
    expect(status == 200 and dashboard["report"]["windowDays"] == 30,
           "an owner can read their institution's dashboard")
    report = dashboard["report"]
    expect(report["members"]["total"] == 1 and report["teaching"]["classes"] == 0,
           "the dashboard counts the remaining member and the institution's classes")
    expect(report["results"]["averageScorePercent"] is None and report["results"]["submissions"] == 0,
           "an institution with no submissions reports no average rather than zero")
    expect(report["classes"] == [], "the class list is an empty array, not null")
    expect(call("GET", f"/organizations/{institution['id']}/report", None, student_token)[0] == 404,
           "a removed member cannot read the dashboard")

    print("class codes")
    status, created = call("POST", "/classes", {"name": "Smoke Codes " + uuid.uuid4().hex[:6]}, teacher_token)
    expect(status == 201 and len(created["class"]["joinCode"]) == 6,
           "a class gets a six-character code that is not derived from its name")
    expect(created["class"].get("joinCodeExpiresAt"), "a class code carries an expiry window")
    code_class = created["class"]["id"]
    first_code = created["class"]["joinCode"]

    student_token = call("POST", "/auth/login", {"email": "student@edumart.ng", "password": "Student123!"})[1]["token"]
    status, _ = call("POST", "/classes/join", {"joinCode": first_code.lower()}, student_token)
    expect(status == 200, "a learner can join with the code, case-insensitively")
    expect(call("POST", f"/classes/{code_class}/join-code", None, student_token)[0] == 403,
           "a learner cannot reissue the code for their own class")

    status, rotated = call("POST", f"/classes/{code_class}/join-code", None, teacher_token)
    expect(status == 200 and rotated["class"]["joinCode"] != first_code,
           "the teacher can reissue a code")
    expect(call("POST", "/classes/join", {"joinCode": first_code}, teacher_token)[0] == 404,
           "rotating a code revokes the previous one")

    print("deliveries")
    admin_token = call("POST", "/auth/login", {"email": "admin@edumart.ng", "password": "Admin123!"})[1]["token"]
    status, records = call("GET", "/admin/deliveries", None, admin_token)
    expect(status == 200 and records["deliveries"], "outbound messages are recorded")
    expect(records["stats"]["sent"] >= 1, "delivered messages are counted as sent")
    expect(all("body" not in d and "code" not in d for d in records["deliveries"]),
           "delivery records never expose message bodies")
    expect(call("GET", "/admin/deliveries", None, token)[0] == 403,
           "learners cannot read delivery records")

    print("reminders")
    admin_token = call("POST", "/auth/login", {"email": "admin@edumart.ng", "password": "Admin123!"})[1]["token"]
    student_token = call("POST", "/auth/login", {"email": "student@edumart.ng", "password": "Student123!"})[1]["token"]
    target = (datetime.now(timezone.utc) + timedelta(days=45)).strftime("%Y-%m-%d")
    status, _ = call("PUT", "/study-plan", {"targetDate": target, "dailyMinutes": 30}, student_token)
    expect(status == 200, "a learner can set a study target")
    status, swept = call("POST", "/admin/reminders/sweep", None, admin_token)
    expect(status == 200 and swept["planned"] >= 1, "an administrator can plan and sweep reminders")
    status, again = call("POST", "/admin/reminders/sweep", None, admin_token)
    expect(status == 200 and again["planned"] == 0, "re-planning schedules nothing new")
    status, queue = call("GET", "/admin/reminders", None, admin_token)
    expect(status == 200 and any(r["kind"] == "study_nudge" for r in queue["reminders"]),
           "the queue shows the reminder that was promised")
    expect(all(r["status"] != "sent" or r["deliveryId"] for r in queue["reminders"]),
           "a sent reminder is tied to the delivery that carried it")
    expect(call("GET", "/admin/reminders", None, student_token)[0] == 403,
           "learners cannot read the reminder queue")
    expect(call("POST", "/admin/reminders/sweep", None, student_token)[0] == 403,
           "learners cannot trigger a sweep")

    print("approvals")
    admin_token = call("POST", "/auth/login", {"email": "admin@edumart.ng", "password": "Admin123!"})[1]["token"]
    status, queue = call("GET", "/admin/organizations?status=pending_verification", None, admin_token)
    expect(status == 200 and isinstance(queue["organizations"], list),
           "an administrator can list institutions awaiting approval")
    status, applied = call("POST", "/organizations", {"name": "Applied Academy " + uuid.uuid4().hex[:6], "kind": "school"}, teacher_token)
    expect(status == 201, "an application is created pending")
    applied_id = applied["organization"]["id"]
    status, queue = call("GET", "/admin/organizations?status=pending_verification", None, admin_token)
    expect(any(o["id"] == applied_id and o.get("ownerEmail") for o in queue["organizations"]),
           "the queue shows the applicant and their contact address")
    expect(call("GET", "/admin/organizations", None, teacher_token)[0] == 403,
           "only platform staff can read the approval queue")
    status, rejected = call("POST", f"/admin/organizations/{applied_id}/reject",
                            {"reason": "smoke: no registration documents"}, admin_token)
    expect(status == 200 and rejected["organization"]["status"] == "rejected",
           "an administrator can refuse an application")
    expect(call("POST", f"/admin/organizations/{applied_id}/reject", {"reason": "again"}, admin_token)[0] == 409,
           "an application cannot be refused twice")

    print("moderation")
    admin_token = call("POST", "/auth/login", {"email": "admin@edumart.ng", "password": "Admin123!"})[1]["token"]
    status, found = call("GET", "/admin/users?q=student@edumart.ng", None, admin_token)
    expect(status == 200 and found["users"], "an administrator can find an account to moderate")
    learner = found["users"][0]
    expect("passwordHash" not in learner and "token" not in learner,
           "account search never exposes credentials")
    expect(call("GET", "/admin/users?q=student", None, token)[0] == 403,
           "learners cannot browse accounts")

    status, _ = call("POST", f"/admin/users/{learner['id']}/suspend",
                     {"reason": "smoke: verifying suspension"}, admin_token)
    expect(status == 200, "an administrator can suspend an account")
    expect(call("GET", "/me", None, token)[0] == 401,
           "suspension ends the sessions the account already holds")
    status, body = call("POST", "/auth/login", {"email": "student@edumart.ng", "password": "Student123!"})
    expect(status == 403 and body["error"]["code"] == "ACCOUNT_SUSPENDED",
           "a suspended account cannot sign in")
    expect(call("POST", f"/admin/users/{learner['id']}/reinstate", {}, admin_token)[0] == 200,
           "an administrator can lift the suspension")
    expect(call("POST", "/auth/login", {"email": "student@edumart.ng", "password": "Student123!"})[0] == 200,
           "a reinstated account can sign in again")

    print("\nsmoke passed")


if __name__ == "__main__":
    main()
