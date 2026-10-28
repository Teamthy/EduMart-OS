"use client";

import { ApiClient } from "@edumart/shared";

/**
 * Browser-side client. Requests go to the same origin (`/v1/...`), where Next.js proxies them to
 * the Go API. The session is an httpOnly cookie, so no token is held in JavaScript.
 */
let client: ApiClient | null = null;

export function api(): ApiClient {
  if (!client) {
    client = new ApiClient({
      baseUrl: "",
      onUnauthenticated: () => {
        if (typeof window !== "undefined" && !window.location.pathname.startsWith("/login")) {
          const next = encodeURIComponent(window.location.pathname + window.location.search);
          window.location.assign(`/login?next=${next}`);
        }
      },
    });
  }
  return client;
}
