import * as SecureStore from "expo-secure-store";
import { Platform } from "react-native";
import Constants from "expo-constants";

import { ApiClient } from "@edumart/shared";

const TOKEN_KEY = "edumart.session";
const REFRESH_KEY = "edumart.session.refresh";

/**
 * The native shell authenticates with a bearer token held in the platform secure store, because React Native
 * has no httpOnly cookie jar. The token is the same opaque session token the browser receives in its cookie,
 * so both shells are revocable from the server in one place.
 *
 * The refresh token is stored beside it. It was not, before: the app kept a seven-day access token and threw
 * the refresh token away, so a phone could never rotate and "single-use rotating refresh tokens" described the
 * test suite rather than either client. The access token is now an hour, which makes keeping the pair a
 * requirement rather than a nicety — without it a learner would be signed out on the bus every hour.
 */
export const tokenStore = {
  async get(): Promise<string | null> {
    if (Platform.OS === "web") return globalThis.localStorage?.getItem(TOKEN_KEY) ?? null;
    return SecureStore.getItemAsync(TOKEN_KEY);
  },
  async set(token: string): Promise<void> {
    if (Platform.OS === "web") {
      globalThis.localStorage?.setItem(TOKEN_KEY, token);
      return;
    }
    await SecureStore.setItemAsync(TOKEN_KEY, token);
  },
  /** The refresh half of the pair, stored separately so a rotated access token never loses it. */
  async getRefresh(): Promise<string | null> {
    if (Platform.OS === "web") return globalThis.localStorage?.getItem(REFRESH_KEY) ?? null;
    return SecureStore.getItemAsync(REFRESH_KEY);
  },
  async setRefresh(token: string): Promise<void> {
    if (Platform.OS === "web") {
      globalThis.localStorage?.setItem(REFRESH_KEY, token);
      return;
    }
    await SecureStore.setItemAsync(REFRESH_KEY, token);
  },
  async clear(): Promise<void> {
    if (Platform.OS === "web") {
      globalThis.localStorage?.removeItem(TOKEN_KEY);
      globalThis.localStorage?.removeItem(REFRESH_KEY);
      return;
    }
    await Promise.all([
      SecureStore.deleteItemAsync(TOKEN_KEY),
      SecureStore.deleteItemAsync(REFRESH_KEY),
    ]);
  },
};

const configuredUrl = (Constants.expoConfig?.extra as { apiUrl?: string } | undefined)?.apiUrl;

/** Absolute API base — the native app cannot use relative URLs like the browser does. */
export const API_URL = (process.env.EXPO_PUBLIC_API_URL ?? configuredUrl ?? "http://localhost:8080").replace(/\/$/, "");

export const api = new ApiClient({
  baseUrl: API_URL,
  getToken: () => tokenStore.get(),
});

let refreshing: Promise<boolean> | null = null;

/**
 * rotateSession exchanges the stored refresh token for a new pair.
 *
 * Single-flight on purpose. A phone that comes back from the background fires several requests at once; if
 * each one refreshed independently, the second exchange would present a token the first had already retired —
 * and the server is right to treat that as theft, ending the session and signing the learner out. One
 * in-flight refresh, shared by every waiting request, is the difference between recovering from expiry and
 * being logged out for it.
 */
export async function rotateSession(): Promise<boolean> {
  if (refreshing) return refreshing;

  refreshing = (async () => {
    const refreshToken = await tokenStore.getRefresh();
    if (!refreshToken) return false;
    try {
      const session = await api.refreshSession(refreshToken);
      await tokenStore.set(session.token);
      await tokenStore.setRefresh(session.refreshToken);
      return true;
    } catch {
      // A rejected refresh means the chain is gone: expired, revoked, or reuse detected. Keeping the tokens
      // would mean retrying a credential the server has already refused.
      await tokenStore.clear();
      return false;
    } finally {
      refreshing = null;
    }
  })();

  return refreshing;
}

/** Signup drafts are anonymous, so the draft token is kept next to (but separate from) the session. */
const DRAFT_KEY = "edumart.signup";

export const draftStore = {
  async get(): Promise<string | null> {
    if (Platform.OS === "web") return globalThis.localStorage?.getItem(DRAFT_KEY) ?? null;
    return SecureStore.getItemAsync(DRAFT_KEY);
  },
  async set(token: string): Promise<void> {
    if (Platform.OS === "web") {
      globalThis.localStorage?.setItem(DRAFT_KEY, token);
      return;
    }
    await SecureStore.setItemAsync(DRAFT_KEY, token);
  },
  async clear(): Promise<void> {
    if (Platform.OS === "web") {
      globalThis.localStorage?.removeItem(DRAFT_KEY);
      return;
    }
    await SecureStore.deleteItemAsync(DRAFT_KEY);
  },
};

/** Draft reads/writes go through the same endpoints the web wizard uses. */
async function draftRequest<T>(method: "GET" | "PATCH" | "POST", path: string, body?: unknown): Promise<T> {
  const token = await draftStore.get();
  const response = await fetch(`${API_URL}${path}`, {
    method,
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const payload = (await response.json()) as T & { error?: { message: string; code: string } };
  if (!response.ok) {
    throw new Error(payload.error?.message ?? "Could not save your progress.");
  }
  return payload;
}

export interface ServerDraft {
  draft: {
    accountType?: "student" | "tutor" | "institution";
    examPathwayId?: string;
    examPathwayKey?: string;
    subjectIds?: string[];
    targetDate?: string;
    dailyMinutes?: number;
    displayName?: string;
    email?: string;
    timezone?: string;
  } | null;
  step: string | null;
  stepIndex: number;
  referralCode?: string;
  token?: string;
}

export const signupApi = {
  current: () => draftRequest<ServerDraft>("GET", "/v1/signup-drafts/current"),
  create: async (referralCode = "") => {
    const result = await draftRequest<ServerDraft>("POST", "/v1/signup-drafts", { referralCode });
    if (result.token) await draftStore.set(result.token);
    return result;
  },
  patch: (patch: Record<string, unknown>) => draftRequest<ServerDraft>("PATCH", "/v1/signup-drafts/current", patch),
};
