"use client";

import { useEffect } from "react";

/**
 * Registers the service worker that makes Edumart installable and usable offline.
 * The worker caches the app shell and static assets only — API responses are never cached, because
 * assessment state must always come from the server.
 */
export function ServiceWorkerRegistration() {
  useEffect(() => {
    if (typeof window === "undefined" || !("serviceWorker" in navigator)) return;
    if (process.env.NODE_ENV !== "production") return;

    const register = () => {
      navigator.serviceWorker.register("/sw.js").catch(() => {
        // Registration failures are non-fatal: the app still works online.
      });
    };
    window.addEventListener("load", register);
    return () => window.removeEventListener("load", register);
  }, []);

  return null;
}
