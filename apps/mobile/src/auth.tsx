import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";

import { isTwoFactorChallenge } from "@edumart/shared";
import type { SessionResponse, User } from "@edumart/shared";

import { api, rotateSession, tokenStore } from "./api";

/** What a sign-in attempt produced: a session in the app, or a challenge that still owes a code. */
export type SignInOutcome = { twoFactor: false } | { twoFactor: true; token: string };

interface AuthState {
  user: User | null;
  loading: boolean;
  signIn: (email: string, password: string) => Promise<SignInOutcome>;
  /** Finish a challenge from {@link signIn} with the code from an authenticator app, or a recovery code. */
  completeSignIn: (challenge: string, code: string) => Promise<void>;
  signOut: () => Promise<void>;
  refresh: () => Promise<void>;
}

const AuthContext = createContext<AuthState | null>(null);

/** Session state for the native app: restore from secure storage, verify against `/v1/me`. */
export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    const token = await tokenStore.get();
    if (!token) {
      setUser(null);
      setLoading(false);
      return;
    }
    try {
      const response = await api.me();
      setUser(response.user);
    } catch (error) {
      // An expired access token is the normal case after an hour asleep, not a sign-out: rotate and retry
      // once before giving up. Anything else is a genuine failure and clears the session.
      const status = (error as { status?: number } | null)?.status;
      if (status === 401 && (await rotateSession())) {
        try {
          const response = await api.me();
          setUser(response.user);
          return;
        } catch {
          /* fall through to signing out */
        }
      }
      await tokenStore.clear();
      setUser(null);
    } finally {
      setLoading(false);
    }
  }, []);

  // adoptSession stores both halves of the pair. Storing only the access token would make an hourly token an hourly
  // sign-in, because the app would have nothing to rotate.
  const adoptSession = useCallback(async (session: SessionResponse) => {
    await tokenStore.set(session.token);
    if (session.refresh_token) await tokenStore.setRefresh(session.refresh_token);
    setUser(session.user);
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  /**
   * Sign in. An account with a second factor gets a challenge back rather than a session, so the caller either lands
   * in the app or receives `{ twoFactor: true }` and shows the code screen. Returning a value rather than throwing is
   * deliberate: nothing failed, and an error banner would tell the person their password was wrong when it was right.
   */
  const signIn = useCallback(async (email: string, password: string): Promise<{ twoFactor: true; token: string } | { twoFactor: false }> => {
    const result = await api.login(email, password, "mobile");
    if (isTwoFactorChallenge(result)) {
      return { twoFactor: true, token: result.token };
    }
    await adoptSession(result);
    return { twoFactor: false };
  }, []);

  /** Second half of a sign-in that owed a factor. Same adoption path once the code is accepted. */
  const completeSignIn = useCallback(async (challenge: string, code: string) => {
    const session = await api.completeLogin(challenge, code.trim(), "mobile");
    await adoptSession(session);
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } finally {
      await tokenStore.clear();
      setUser(null);
    }
  }, []);

  const value = useMemo(
    () => ({ user, loading, signIn, completeSignIn, signOut, refresh }),
    [user, loading, signIn, completeSignIn, signOut, refresh],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useAuth must be used inside AuthProvider");
  return context;
}
