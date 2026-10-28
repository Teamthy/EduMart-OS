/** Shape returned by every wizard server action. Kept out of the "use server" module on purpose. */
export interface StepState {
  ok: boolean;
  errors: Record<string, string>;
  message?: string;
  requestId?: string;
}

export const EMPTY_STATE: StepState = { ok: false, errors: {} };
