import type { NextConfig } from "next";

/**
 * Security headers are applied in every environment except framing controls, which are
 * enabled only in production: the hosted preview runs the app inside an iframe on another
 * origin, and a blanket SAMEORIGIN policy would block it there.
 */
const isProd = process.env.NODE_ENV === "production";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  poweredByHeader: false,
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
          { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=()" },
          { key: "X-DNS-Prefetch-Control", value: "off" },
          ...(isProd
            ? [
                { key: "X-Frame-Options", value: "SAMEORIGIN" },
                { key: "Content-Security-Policy", value: "frame-ancestors 'self'" },
                { key: "Strict-Transport-Security", value: "max-age=31536000; includeSubDomains" },
              ]
            : []),
        ],
      },
    ];
  },
};

export default nextConfig;
