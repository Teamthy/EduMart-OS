# Synchronised exams

An exam here is a **sitting**: one paper, one window, everybody starting inside the same period with the same
duration, each candidate seeing the same questions in their own order. This document records what is enforced,
where, and what is not built.

## The shape of a sitting

| Concern | Where it lives | Enforced by |
| --- | --- | --- |
| The paper | `assessment_definitions` + `assessment_items` | the create endpoint, from approved and published questions only |
| The window | `availability_start` / `availability_end` | `StartDefinition` refuses a start outside it |
| The duration | `duration_seconds`, clamped at `availability_end` | the deadline stored on the attempt |
| The permutation | `assessment_sessions.snapshot_json` | seeded by `(definition, user)` at start |
| The attempt | `assessment_sessions` | one row per candidate per attempt, `attempt_limit` attempts |
| The answers | `assessment_responses` | server-side only; keys never leave the server before release |
| The score | `assessment_sessions.score_*` | one finalisation path for submit and expiry |

An exam is not a second assessment engine. The engine in `internal/assessment` already owns the snapshot, the
deadline, the scoring policy and the single finalisation path; `internal/exams` is the sitting around it.

## "The same exam at the same time"

- **The same questions.** Every attempt is served from an immutable per-attempt snapshot. Two candidates get the
  same set because it is the same paper, and a later correction to a source question cannot rewrite a completed
  attempt.
- **At the same time.** An attempt can only start inside the window. `POST /v1/exams/{id}/start` returns `409`
  with `ASSESSMENT_NOT_OPEN` before the window opens and after it closes.
- **The same duration, and the window wins.** An attempt that starts late ends when the window closes; the
  deadline stored on the attempt is `min(started + duration, availability_end)`. A deadline the server would not
  accept an answer past is worse than an honest short one.
- **Shuffled, per candidate.** With `shuffle: true`, questions and their options are permuted with a seed of
  `(examId, userId)`. Two neighbours see different orders; one candidate who reloads sees *their* order again,
  so answers already given still line up.
- **One attempt.** `attempt_limit` is 1 for a sitting, enforced inside the start path, not by hiding a button.

## End to end, recorded by the server

| Moment | Recorded | Where |
| --- | --- | --- |
| The candidate starts | `started_at`, `deadline_at`, attempt number, the snapshot | `assessment_sessions` |
| Each answer | the chosen option, a version counter, the save time | `assessment_responses` |
| The candidate submits | `submitted_at`, `score_raw`, `score_max`, `score_percent` | `assessment_sessions` |
| The deadline passes first | the same fields, with status `expired` | `ExpireDue` / the expiry worker |
| The candidate's tab loses focus | the signal, our receipt time, and the client's claim | `exam_integrity_events` |
| The host looks | start, submit, score, remaining seconds, and the integrity tally per candidate | `GET /v1/exams/{id}/results` |

A candidate never sees another candidate's attempt: `GET /v1/assessment-sessions/{id}` is scoped to its owner,
and the sitting report is host-only (`403` for everybody else).

## The unique link

`slug` is 16 characters from `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`, unique across published definitions (partial
unique index, so the many definitions without a slug do not collide). The link is
`<EDUMART_WEB_URL>/exams/<slug>`.

`GET /v1/exams/link/{slug}` is the anonymous preview: what the exam is, who set it, when the window opens and
closes, how long it lasts, how many questions — and nothing about the paper. A public endpoint that leaked
question text would be a revision aid for exactly the wrong person.

## Simulation is not a sitting

"Exact exam simulation" (`POST /v1/practice-sessions` with `mode: "simulation"`) is deliberately a *different*
feature from the synchronised sitting above, and the difference is worth stating so nobody builds the wrong one:

| | Simulation | Sitting |
| --- | --- | --- |
| Others involved | none — you sit your own paper | everyone in the same window |
| Paper | fixed per caller: the same selection returns the same paper | one paper for the cohort, permuted per candidate |
| Clock | `durationMinutes`, 5–240 | fixed by the host, deadline `min(started+duration, endsAt)` |
| Corrections | refused | refused |
| Results | yours, immediately after submit | host sees everybody; release follows the sitting's policy |

A simulation reuses the same snapshot, the same `domain.ScoreAttempt` and the same "no corrections" rule, which is
what makes it a rehearsal rather than a mini-game: the only things it removes are the other people.

## Integrity signals: attention, recorded and reported

A synchronised sitting has a deadline and a shuffled paper, which stops a neighbour's screen being copied. It
does not stop a candidate leaving the tab for four minutes and coming back with an answer. So the candidate's
browser reports on itself, and the server records what it said:

| Signal | When the client sends it |
| --- | --- |
| `focus_lost` / `focus_back` | the tab was hidden or the window lost focus, and came back |
| `fullscreen_exit` | fullscreen was left during the paper |
| `paste` / `copy` | text arrived from or left for outside the paper (a field marked `data-allow-paste` is exempt) |
| `offline` / `back_online` | the network dropped mid-question |

Four rules make the record worth reading:

1. **The vocabulary is closed.** `POST /v1/assessment-sessions/{id}/integrity` accepts seven kinds and nothing
   else, because the value is rendered to a host; an open list would be arbitrary text in their report.
2. **Only the owner's open attempt.** Recording into somebody else's attempt is `403`, and into a closed one is
   `409` — a "distraction" cannot be written after submission.
3. **Two clocks, not one.** `occurred_at` is what the client claimed; `received_at` is when we saw it. The
   difference is itself a signal, and normalising one into the other would erase it.
4. **It never blocks answering.** Reporting is best-effort in the runner: a failed telemetry call is swallowed, so
   a learner never loses a question because a signal did not send.

A signal is evidence, not a verdict: six lost-tab events beside a clean score is a different result from a clean
score with none, and the tally is painted rather than decided. But a report arrives after the paper, and by then
nothing can be done about it. That is what the proctor's desk is for.

## Proctoring: watching, speaking, annulling

A proctor is somebody who can act **while the sitting is open**. Four verbs, and nothing else is offered:

| Verb | Route | What it does |
| --- | --- | --- |
| **Watch** | `GET /v1/exams/{id}/proctor` | Every attempt in the sitting: progress, signal counts, the latest of (last saved answer, last reported signal, start), warnings sent and unread, and the intervention log. Host only |
| **Speak** | `POST /v1/exams/{id}/proctor/warnings` | One warning to explicit session ids. `attention` and `time` are composed by the server from the attempt's own tally and remaining seconds; `notice` carries the host's words (3–250 chars) |
| **Annul** | `POST /v1/exams/{id}/proctor/void` | Sets the attempt to terminal `voided`, with a required reason. The engine then refuses answers, signals and submission |
| **Undo** | `POST /v1/exams/{id}/proctor/reopen` | Reverses a void while the sitting is open and the attempt's own deadline has not passed, and logs that it did |

The candidate's side is two routes: `GET /v1/assessment-sessions/{id}/warnings` (polled by the runner every eight
seconds while the paper is open) and `POST .../warnings/{warningId}/acknowledge`. An unread warning is a
non-dismissible banner over the paper — the acknowledgement is what clears it, and the host's desk shows that it
was read.

Four decisions are what make this proctoring rather than a dashboard:

1. **Two of the three warning kinds are written by the server.** `attention` says "your attempt has recorded 2
   signals the proctor can see: the tab left the paper 2 times". A host cannot send a candidate a punishment no
   table supports, because the sentence is composed from the table.
2. **`voided` is its own terminal status**, not `cancelled`. An annulled attempt is a different fact from one the
   candidate walked away from, so it stays in the host's report — with the actor and the reason — instead of
   disappearing from it. The action log (`exam_proctor_actions`) and `audit_logs` both get a row.
3. **Nothing is automatic.** There is no threshold, no score and no rule that annuls a paper. A person presses the
   button and their user id is written next to the decision, because an algorithm that fails a candidate produces
   an appeal nobody can answer.
4. **A warning is not a punishment.** It is a message that can be read, acknowledged and argued with, and its
   effect on the record is that the host knows it arrived.

What proctoring here is **not**:

- **Not identity verification.** A sitting has no media session — it is a browser and a deadline — so nothing in
  this feature can say who is at the keyboard. There is no webcam capture, no ID check and no keystroke
  biometrics, and a modified client can under-report its own signals. The desk shows what the candidate's browser
  reported beside what the server recorded; the judgement belongs to the person in front of it.
- **Not a lockdown browser.** The runner does not block other applications, second screens or a phone under the
  desk. It records attention and lets a proctor intervene.
- **Not a reason on the candidate's screen.** The void reason is written for the report and the audit trail. The
  candidate is told their attempt was annulled and that the host can reopen it while the sitting is still open.

### Proved, not asserted

| Claim | Where it is checked |
| --- | --- |
| The desk shows the server's numbers, and only the host may read it | `TestProctorWatchesSpeaksAndAnnuls`, `scripts/proctor-lifecycle.py` step 1 |
| A warning quotes the attempt's own tally; a closed or foreign attempt is refused | same test / script step 2 |
| Reading a warning is a fact the desk can see, and acknowledging twice does not move it | same test / script step 3 |
| A void is enforced by the engine (answers `409`, signals `409`), not by the UI | same test / script step 4 |
| A void is reversible while the sitting is open, and a submitted paper is never voidable | `TestProctorCannotAnnulAHandedInPaper` |
| Every intervention is a row in the action log *and* in `audit_logs` | same test / script step 5 (reads the database) |

## What is not built

- **No proctoring of identity.** Proctoring here is a host watching, warning and annulling; it is not a webcam
  check, an ID check or keystroke biometrics, and a modified client can under-report its own signals.
- **No A/V recording of a sitting.** Class recordings exist (a host can capture a live class, sealed in the browser),
  but a sitting is not a room: there is no media session to capture, so the integrity record stays data — signals the
  candidate's own browser reported — rather than video. Recording a candidate's camera would be a different feature
  with a different consent problem, and it is not built.
- **No partial credit** beyond the engine's scoring policy (correct / wrong / unanswered weights).
- **No question authoring in the sitting flow.** A sitting draws from approved, published question versions, or
  names them by id; authoring and review stay with the content pipeline.
- **Results are released immediately** (`result_release: 'immediate'`). A "release at a time" policy exists in
  the engine's schema but has no UI yet.
