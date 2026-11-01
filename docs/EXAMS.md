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

What it is **not**: video proctoring. Nothing here proves who was at the keyboard, and a client that lies leaves
no trace. It is evidence a host weighs — six lost-tab events beside a clean score is a different result from a
clean score with none — and the report paints the tally rather than deciding for them.

## What is not built

- **No proctoring of identity.** Integrity signals record attention, not who was sitting the paper: there is no
  webcam capture, no ID check and no keystroke biometrics, and a modified client can under-report.
- **No A/V recording of a sitting.** The class media server forwards packets and writes nothing to disk; the
  integrity record is data, not video.
- **No partial credit** beyond the engine's scoring policy (correct / wrong / unanswered weights).
- **No question authoring in the sitting flow.** A sitting draws from approved, published question versions, or
  names them by id; authoring and review stay with the content pipeline.
- **Results are released immediately** (`result_release: 'immediate'`). A "release at a time" policy exists in
  the engine's schema but has no UI yet.
