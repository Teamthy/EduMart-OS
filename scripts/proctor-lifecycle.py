#!/usr/bin/env python3
"""Prove proctoring end to end: a host watching, speaking, annulling and undoing, over HTTP.

Proctoring is the one feature whose value is entirely in the verbs. A report that a candidate lost the tab six
times is worth reading afterwards; a proctor is somebody who can do something while the paper is still open. This
script drives a real server through the whole sequence and checks the parts that are easy to fake:

  1. **The desk is the sitting.** Two candidates start, one answers and reports attention; the host sees both with
     progress, signal counts and a last-seen time, and each of them is refused the desk with 403.
  2. **A warning quotes the server.** The `attention` warning the candidate reads contains the number of signals
     the server actually recorded, and an attempt outside the sitting is a 404 rather than a silent no-op.
  3. **Reading it is a fact.** The candidate polls, sees it unread, acknowledges, and the host's desk stops
     counting it as unread. A second acknowledgement does not move the timestamp.
  4. **Annulling is enforcement, not display.** After the void, an answer write is 409 and a new signal is 409 —
     the assessment engine refuses them — while the attempt stays in the report as `voided` with a reason.
  5. **A void is reversible, in the open, on the record.** Reopen puts the attempt back and it accepts answers
     again; an attempt that was never voided cannot be "reopened". Every intervention leaves a row in
     `exam_proctor_actions` and in `audit_logs`, checked by reading the database directly.

Usage:
    python3 scripts/proctor-lifecycle.py --base http://127.0.0.1:8086 --db /tmp/proctor.db

It starts nothing. Point it at a running API and at the SQLite file that API is using: the script seeds the paper
the way a reviewer would, and at the end it reads the two tables the API wrote, because "audited" is a claim about
rows and not about a response body.
"""

from __future__ import annotations

import argparse
import http.cookiejar
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone

FAILURES: list[str] = []

OPTIONS = json.dumps(
    [
        {"id": "opt_1", "label": "A. First option"},
        {"id": "opt_2", "label": "B. Second option"},
        {"id": "opt_3", "label": "C. Third option"},
        {"id": "opt_4", "label": "D. Fourth option"},
    ]
)


def check(condition: bool, message: str) -> None:
    if condition:
        print(f"  ok   {message}")
        return
    FAILURES.append(message)
    print(f"  FAIL {message}")


def now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


class Client:
    """A cookie jar and a bearer token, which is how both clients talk to the API."""

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


def _json(raw: bytes) -> dict:
    if not raw:
        return {}
    try:
        return json.loads(raw.decode())
    except ValueError:
        return {}


def register(client: Client, email: str, name: str) -> str:
    status, body = client.call(
        "POST",
        "/v1/auth/register",
        {"displayName": name, "email": email, "password": "Str0ngPassw0rd!", "client": "web"},
    )
    if status not in (200, 201):
        raise SystemExit(f"register {email}: {status} {body}")
    client.token = body["token"]
    return body["user"]["id"]


def seed_paper(db_path: str, subject_id: str, count: int) -> list[str]:
    """Insert approved, published questions the way a reviewer would, straight into the database.

    The API has no authoring route in this build (content ops are a separate phase), and an exam without an
    approved paper cannot be created at all — so the script writes the rows the reviewer step would write, then
    drives everything else over HTTP.
    """
    stamp = now()
    ids: list[str] = []
    connection = sqlite3.connect(db_path)
    try:
        for index in range(count):
            question_id = f"q_proctor_{os.getpid()}_{index}"
            version_id = f"qv_proctor_{os.getpid()}_{index}"
            connection.execute(
                "INSERT INTO questions (id, subject_id, type, difficulty, status, created_at)"
                " VALUES (?,?,'mcq','medium','published',?)",
                (question_id, subject_id, stamp),
            )
            connection.execute(
                "INSERT INTO question_versions (id, question_id, version, stem, options_json, answer_option_id,"
                " explanation, source_name, license_basis, attribution, review_state, published_at, created_at)"
                " VALUES (?,?,1,?,?,'opt_2','Because the second option is correct.','Exam bank','licensed',"
                " 'Proctor lifecycle','approved',?,?)",
                (version_id, question_id, f"Proctored question {index + 1}: which option is correct?", OPTIONS, stamp, stamp),
            )
            ids.append(version_id)
        connection.commit()
    finally:
        connection.close()
    return ids


def count_rows(db_path: str, sql: str) -> int:
    connection = sqlite3.connect(db_path)
    try:
        return int(connection.execute(sql).fetchone()[0])
    finally:
        connection.close()


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default=os.environ.get("EDUMART_BASE_URL", "http://127.0.0.1:8080"))
    parser.add_argument("--db", default=os.environ.get("EDUMART_DSN", "/tmp/proctor.db"),
                        help="Path to the SQLite file the API is using (EDUMART_DSN when it is a plain path).")
    args = parser.parse_args()

    print("0. an open sitting with an approved paper")
    host = Client(args.base)
    register(host, f"proctor-host-{os.getpid()}@example.com", "Proctor Host")
    first = Client(args.base)
    register(first, f"proctor-first-{os.getpid()}@example.com", "First Candidate")
    second = Client(args.base)
    register(second, f"proctor-second-{os.getpid()}@example.com", "Second Candidate")

    status, body = host.call("GET", "/v1/subjects")
    check(status == 200, f"the subject list is readable ({status})")
    subjects = body.get("subjects") or []
    mathematics = next((item for item in subjects if item.get("slug") == "mathematics"), None)
    if mathematics is None:
        mathematics = subjects[0] if subjects else None
    if mathematics is None:
        raise SystemExit("no subjects in this database — seed it first")
    questions = seed_paper(args.db, mathematics["id"], 3)

    start = datetime.now(timezone.utc) - timedelta(minutes=1)
    status, body = host.call(
        "POST",
        "/v1/exams",
        {
            "title": "Proctored sitting lifecycle",
            "durationMinutes": 20,
            "startsAt": start.strftime("%Y-%m-%dT%H:%M:%SZ"),
            "endsAt": (start + timedelta(minutes=30)).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "shuffle": True,
            "questionVersionIds": questions,
        },
    )
    check(status == 201, f"the host publishes a sitting inside its window ({status})")
    exam = body["exam"]
    exam_id = exam["id"]
    check(exam.get("open") is True, "the sitting is open")

    status, first_attempt = first.call("POST", f"/v1/exams/{exam_id}/start")
    check(status == 201, f"the first candidate starts ({status})")
    first_session = first_attempt["session_id"]
    status, second_attempt = second.call("POST", f"/v1/exams/{exam_id}/start")
    check(status == 201, f"the second candidate starts ({status})")
    second_session = second_attempt["session_id"]
    qv = first_attempt["questions"][0]["qvId"]

    print("1. the desk is the sitting, and only for its host")
    status, _ = first.call("PUT", f"/v1/assessment-sessions/{first_session}/responses/{qv}",
                           {"selectedOptionId": "opt_2"})
    check(status == 200, f"the first candidate answers one question ({status})")
    for _ in range(2):
        first.call("POST", f"/v1/assessment-sessions/{first_session}/integrity",
                   {"kind": "focus_lost", "detail": "window hidden"})

    status, desk = host.call("GET", f"/v1/exams/{exam_id}/proctor")
    check(status == 200, f"the host reads the desk ({status})")
    sitting = desk.get("sitting") or {}
    rows = desk.get("candidates") or []
    check(len(rows) == 2, f"the desk lists both attempts ({len(rows)})")
    check(sitting.get("inProgress") == 2, f"both are counted as sitting ({sitting.get('inProgress')})")
    check(sitting.get("flagged") == 1, f"exactly one attempt is flagged ({sitting.get('flagged')})")

    watched = next((row for row in rows if row["sessionId"] == first_session), None)
    if watched is None:
        raise SystemExit("the desk did not list the first attempt")
    check(watched["answered"] == 1, f"progress is what the server recorded ({watched['answered']})")
    check(watched["questions"] == 3, f"the desk knows the length of the paper ({watched['questions']})")
    check((watched.get("integrity") or {}).get("signals") == 2,
          f"the signal count is the server's own ({watched.get('integrity')})")
    check(bool(watched.get("lastSeenAt")), f"last seen is a real timestamp ({watched.get('lastSeenAt')})")

    status, _ = first.call("GET", f"/v1/exams/{exam_id}/proctor")
    check(status == 403, f"a candidate is refused the desk ({status})")
    status, _ = first.call("POST", f"/v1/exams/{exam_id}/proctor/warnings",
                           {"sessionIds": [second_session], "kind": "attention"})
    check(status == 403, f"a candidate cannot warn another candidate ({status})")

    print("2. a warning the server can stand behind")
    status, _ = host.call("POST", f"/v1/exams/{exam_id}/proctor/warnings",
                          {"sessionIds": [first_session], "kind": "made_up"})
    check(status == 400, f"an unknown warning kind is refused ({status})")
    status, _ = host.call("POST", f"/v1/exams/{exam_id}/proctor/warnings",
                          {"sessionIds": ["sesn_does_not_exist"], "kind": "attention"})
    check(status == 404, f"warning an attempt outside the sitting is a 404 ({status})")
    status, _ = host.call("POST", f"/v1/exams/{exam_id}/proctor/warnings",
                          {"sessionIds": [first_session], "kind": "notice", "note": "hi"})
    check(status == 400, f"a notice without real words is refused ({status})")

    status, body = host.call("POST", f"/v1/exams/{exam_id}/proctor/warnings",
                             {"sessionIds": [first_session], "kind": "attention"})
    check(status == 201, f"the host warns one candidate ({status})")
    note = body["warnings"][0]["note"]
    check("2 signal" in note, f"the warning quotes the server's tally ({note!r})")

    print("3. reading it is a fact the host can see")
    status, body = first.call("GET", f"/v1/assessment-sessions/{first_session}/warnings")
    check(status == 200, f"the candidate polls their warnings ({status})")
    check(body.get("unread") == 1, f"one warning is unread ({body.get('unread')})")
    warning_id = body["warnings"][0]["id"]

    status, _ = second.call("GET", f"/v1/assessment-sessions/{first_session}/warnings")
    check(status == 403, f"another candidate cannot read them ({status})")

    status, body = first.call("POST",
                              f"/v1/assessment-sessions/{first_session}/warnings/{warning_id}/acknowledge")
    check(status == 200, f"the candidate acknowledges ({status})")
    first_ack = body["warning"]["acknowledgedAt"]
    check(bool(first_ack), f"the acknowledgement is stamped ({first_ack})")
    status, body = first.call("POST",
                              f"/v1/assessment-sessions/{first_session}/warnings/{warning_id}/acknowledge")
    check(body["warning"]["acknowledgedAt"] == first_ack,
          "a second acknowledgement does not move the first one")

    status, desk = host.call("GET", f"/v1/exams/{exam_id}/proctor")
    watched = next(row for row in desk["candidates"] if row["sessionId"] == first_session)
    check(watched["unreadWarnings"] == 0, f"the desk stops counting it unread ({watched['unreadWarnings']})")
    check((watched.get("lastWarning") or {}).get("acknowledgedAt") is not None,
          "the desk shows the warning was read")

    print("4. annulling is enforcement")
    status, _ = host.call("POST", f"/v1/exams/{exam_id}/proctor/void",
                          {"sessionId": first_session, "reason": "no"})
    check(status == 400, f"a void needs a reason worth reading ({status})")
    status, body = host.call("POST", f"/v1/exams/{exam_id}/proctor/void",
                             {"sessionId": first_session, "reason": "Photographing the paper for a group chat"})
    check(status == 200, f"the host annuls the attempt ({status})")
    check(body["candidate"]["status"] == "voided",
          f"the attempt reports its own terminal status ({body['candidate']['status']})")
    check("Photographing" in (body["candidate"].get("voidReason") or ""),
          "the reason travels with the annulled attempt")

    status, _ = first.call("PUT", f"/v1/assessment-sessions/{first_session}/responses/{qv}",
                           {"selectedOptionId": "opt_3"})
    check(status == 409, f"an annulled attempt refuses answers ({status})")
    status, _ = first.call("POST", f"/v1/assessment-sessions/{first_session}/integrity",
                           {"kind": "focus_lost"})
    check(status == 409, f"an annulled attempt refuses new signals ({status})")
    status, _ = host.call("POST", f"/v1/exams/{exam_id}/proctor/void",
                          {"sessionId": first_session, "reason": "and again"})
    check(status == 409, f"it cannot be annulled twice ({status})")

    status, body = host.call("GET", f"/v1/exams/{exam_id}/results")
    statuses = {row["status"] for row in body.get("candidates") or []}
    check("voided" in statuses, f"the annulled attempt stays in the report ({sorted(statuses)})")

    print("5. undoing a void, on the record")
    status, _ = host.call("POST", f"/v1/exams/{exam_id}/proctor/reopen", {"sessionId": second_session})
    check(status == 409, f"an attempt that was never annulled cannot be reopened ({status})")
    status, body = host.call("POST", f"/v1/exams/{exam_id}/proctor/reopen",
                             {"sessionId": first_session, "reason": "annulled the wrong attempt"})
    check(status == 200, f"the host reopens it ({status})")
    check(body["candidate"]["status"] == "in_progress",
          f"the reopened attempt is live again ({body['candidate']['status']})")
    status, _ = first.call("PUT", f"/v1/assessment-sessions/{first_session}/responses/{qv}",
                           {"selectedOptionId": "opt_3"})
    check(status == 200, f"and it accepts answers again ({status})")

    actions = count_rows(args.db, "SELECT COUNT(*) FROM exam_proctor_actions")
    check(actions == 3, f"every intervention left a row in the action log ({actions})")
    audited = count_rows(args.db, "SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'exam.proctor.%'")
    check(audited == 3, f"every intervention is audited ({audited})")

    print()
    if FAILURES:
        print(f"{len(FAILURES)} check(s) failed:")
        for failure in FAILURES:
            print(f"  - {failure}")
        return 1
    print("proctor lifecycle verified")
    return 0


if __name__ == "__main__":
    sys.exit(main())
