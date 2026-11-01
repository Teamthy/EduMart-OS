import type { NextConfig } from "next";

/**
 * The browser never talks to the Go API directly: `/v1/*` is proxied so the session cookie stays
 * first-party (httpOnly, SameSite=Lax) and the same bundle works behind any host without CORS.
 *
 * The proxy itself is a **route handler** (`app/v1/[...path]/route.ts`), not a rewrite. A rewrite's
 * destination is resolved by `next build` and frozen into the routes manifest, so `EDUMART_API_URL` would
 * have had one lifetime at build time for `/v1/*` and another at run time for server-rendered pages —
 * measured on a real standalone build, that produced containers that half worked. See the comment at the
 * top of the route handler.
 */

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // The container image runs Next's own server (`node server.js`) with only the files it actually needs,
  // instead of `next start` over the whole project. Without this the image either ships the entire
  // node_modules tree or has to install dependencies at start-up, which is a network dependency in the
  // middle of a rollout.
  output: "standalone",
  poweredByHeader: false,
  transpilePackages: ["@edumart/shared"],
  async headers() {
    const securityHeaders = [
      { key: "X-Content-Type-Options", value: "nosniff" },
      { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
      { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=()" },
    ];
    // Framing rules are production-only: the hosted preview renders the app inside an iframe.
    if (process.env.NODE_ENV === "production" && process.env.EDUMART_ALLOW_FRAMING !== "true") {
      securityHeaders.push(
        { key: "X-Frame-Options", value: "SAMEORIGIN" },
        { key: "Content-Security-Policy", value: "frame-ancestors 'self'" },
        { key: "Strict-Transport-Security", value: "max-age=63072000; includeSubDomains" },
      );
    }
    return [{ source: "/:path*", headers: securityHeaders }];
  },
};

export default nextConfig;
