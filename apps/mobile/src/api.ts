import * as SecureStore from "expo-secure-store";
import { Platform } from "react-native";
import Constants from "expo-constants";

import { ApiClient } from "@edumart/shared";

const TOKEN_KEY = "edumart.session";

/**
 * The native shell authenticates with a bearer token held in the platform secure store, because
 * React Native has no httpOnly cookie jar. The token is the same opaque session token the browser
 * receives in its cookie, so both shells are revocable from the server in one place.
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
  async clear(): Promise<void> {
    if (Platform.OS === "web") {
      globalThis.localStorage?.removeItem(TOKEN_KEY);
      return;
    }
    await SecureStore.deleteItemAsync(TOKEN_KEY);
  },
};

const configuredUrl = (Constants.expoConfig?.extra as { apiUrl?: string } | undefined)?.apiUrl;

/** Absolute API base — the native app cannot use relative URLs like the browser does. */
export const API_URL = (process.env.EXPO_PUBLIC_API_URL ?? configuredUrl ?? "http://localhost:8080").replace(/\/$/, "");

export const api = new ApiClient({
  baseUrl: API_URL,
  getToken: () => tokenStore.get(),
});

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
