import type { NextConfig } from "next";

/**
 * The browser never talks to the Go API directly. Next.js proxies `/v1/*` to the API service, which
 * keeps the session cookie first-party (so it stays httpOnly and SameSite=Lax) and means the same
 * bundle works behind any preview host without CORS configuration.
 */
const apiUrl = process.env.EDUMART_API_URL ?? "http://127.0.0.1:8080";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  transpilePackages: ["@edumart/shared"],
  async rewrites() {
    return [
      { source: "/v1/:path*", destination: `${apiUrl}/v1/:path*` },
      { source: "/healthz", destination: `${apiUrl}/healthz` },
    ];
  },
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
