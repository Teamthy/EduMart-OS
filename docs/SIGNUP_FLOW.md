# The signup flow

Five pages, one account, no lost work.

```
/register/pathway   →  /register/subjects  →  /register/schedule  →  /register/account  →  /register/ready
   which exam            which subjects         date + daily time       name/email/password     plan summary
        │                     │                      │                        │                     │
        └─────────────── PATCH /v1/signup-drafts/current (server) ────────────┘                     │
                                                       │                                          │
                                                       └── POST /v1/auth/register → session ───────┘
```

## Why it is server-persisted

The wizard's answers live in `signup_drafts`, keyed by an opaque 256-bit token held in an httpOnly
cookie on the web and in the OS secure store on mobile. Nothing about progress is stored in
`localStorage` or in React state alone, which means:

- A refresh, a crash, a dead battery or a dropped connection resumes **on the same page**.
- The learner can start on the phone and finish on a laptop (or the reverse): the draft is the same
  row.
- The account is created from one already-validated payload, so half-registered users cannot exist.
- Validation runs on both sides from the **same definitions** (`packages/shared/src/signup.ts`), so
  the messages the learner sees match what the API enforces.

`signup_drafts` holds no password. The password exists only in the request body of
`POST /v1/auth/register`, in the final step.

## Step contracts

| Step | Fields written | Server-side validation |
| --- | --- | --- |
| 1 `pathway` | `examPathwayId`, `examPathwayKey` | Required; must be an active pathway |
| 2 `subjects` | `subjectIds[]` | At least one; ids must exist and be active |
| 3 `schedule` | `targetDate`, `dailyMinutes` | Date not in the past and ≤ 2 years out; 10–240 minutes |
| 4 `account` | `displayName`, `email`, `referralCode` | Name ≥ 2 chars; email format; password ≥ 8 with a letter and a digit |
| 5 `confirm` | — | Creates the account, links the referral, marks the draft complete |

## Entry and resume rules

- `/register` reads the draft. If one exists it redirects to the saved step; otherwise it offers to
  start (forwarding `?ref=CODE`).
- Any step page without a draft redirects back to `/register` rather than showing an empty form.
- `/register/ready` requires a session; it is a summary, not a placeholder.
- An unknown `currentStep` is rejected with `400 VALIDATION_ERROR` so a stale client cannot corrupt
  its own progress.

## Cross-platform parity

Both shells drive the same endpoints:

| Concern | Web | Mobile |
| --- | --- | --- |
| Step pages | `apps/web/app/register/*` | `apps/mobile/app/signup/*` |
| Draft token | httpOnly cookie | `expo-secure-store` (`edumart.signup`) |
| Validation | `validateStep()` from `@edumart/shared` | identical import |
| Step copy | `SIGNUP_STEPS` from `@edumart/shared` | identical import |
| Final account | server action → `POST /v1/auth/register` | direct call with bearer token |

## Tested behaviour

`TestSignupWizardResumesFromServerState` (Go) covers: draft creation, step advance, re-read after a
"refresh" returning identical answers, account creation from the draft, and rejection of an unknown
step. `tests/signup-flow.test.ts` covers step validation, ordering and payload normalisation on the
client side.
