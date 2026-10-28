import type { Draft } from "./signup-types";
import { ServerApiError, serverFetch } from "./server-api";

export interface DraftResponse {
  draft: Draft | null;
  step: string | null;
  stepIndex: number;
  referralCode?: string;
  /** Present only on creation: the opaque draft token the wizard stores in a cookie. */
  token?: string;
}

/** Read the wizard draft for the current browser (server-side, cookie-forwarded). */
export async function readDraft(): Promise<DraftResponse> {
  try {
    return await serverFetch<DraftResponse>("/v1/signup-drafts/current");
  } catch (error) {
    if (error instanceof ServerApiError && error.status === 401) {
      return { draft: null, step: null, stepIndex: 0 };
    }
    throw error;
  }
}

/**
 * Patch the draft. Called from server actions so the progress write happens on the server even
 * when the client's JavaScript has not loaded yet.
 */
export async function patchDraft(patch: Record<string, unknown>): Promise<DraftResponse> {
  return serverFetch<DraftResponse>("/v1/signup-drafts/current", { method: "PATCH", body: patch });
}

export async function createDraft(referralCode = ""): Promise<DraftResponse> {
  return serverFetch<DraftResponse>("/v1/signup-drafts", { method: "POST", body: { referralCode } });
}
