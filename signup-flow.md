# Five-step stateful signup

1. **Account** — email or phone, password, confirmation/consent as required.
2. **Verify** — verification code or verification-link completion. In development, use a clearly marked mock adapter only.
3. **Profile** — display name and minimal required profile data.
4. **Learning setup** — learner/teacher/institution intent, exam type, subjects, optional target date. Role selection must not grant privileged access; institutional privileges require verified invitation/approval.
5. **Review and create** — show submitted choices, accept required policies, send one idempotent registration request, then route to the correct onboarding/dashboard state.

## State model

`currentStep`, `formData`, `fieldErrors`, `isSubmitting`, `serverError`, `draftUpdatedAt`, `verificationState`.

- Validate current step before moving forward.
- Preserve valid entered values on back navigation.
- Save only non-sensitive draft fields locally. Never store passwords, verification codes, access tokens, or secrets in localStorage/AsyncStorage.
- Web: optional session-scoped draft persistence for non-sensitive fields; clear it after successful registration.
- Mobile: secure storage for tokens only; ordinary draft data may use local storage after privacy review.
- Backend performs final validation and authoritative account creation.
- Registration request is idempotent to prevent duplicate accounts after timeout/retry.
- Display clear errors and allow retry without losing non-sensitive input.
