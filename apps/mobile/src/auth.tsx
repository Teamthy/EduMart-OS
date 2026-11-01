import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";

import type { User } from "@edumart/shared";

import { api, rotateSession, tokenStore } from "./api";

interface AuthState {
  user: User | null;
  loading: boolean;
  signIn: (email: string, password: string) => Promise<void>;
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

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const signIn = useCallback(async (email: string, password: string) => {
    const session = await api.login(email, password, "mobile");
    await tokenStore.set(session.token);
    // Both halves of the pair, or the app can never rotate and an hourly token becomes an hourly sign-in.
    if (session.refresh_token) await tokenStore.setRefresh(session.refresh_token);
    setUser(session.user);
  }, []);

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } finally {
      await tokenStore.clear();
      setUser(null);
    }
  }, []);

  const value = useMemo(() => ({ user, loading, signIn, signOut, refresh }), [user, loading, signIn, signOut, refresh]);
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const context = useContext(AuthContext);
  if (!context) throw new Error("useAuth must be used inside AuthProvider");
  return context;
}
