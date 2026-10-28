import type { Metadata, Viewport } from "next";

import "./globals.css";
import { ServiceWorkerRegistration } from "@/components/layout/service-worker";

export const metadata: Metadata = {
  metadataBase: new URL(process.env.NEXT_PUBLIC_SITE_URL ?? "http://localhost:3000"),
  title: {
    default: "Edumart — pass your exam with practice that adapts",
    template: "%s · Edumart",
  },
  description:
    "Timed mock exams, adaptive practice and verified results for JAMB, WAEC and NECO. Built for Nigerian learners and the way they actually study.",
  applicationName: "Edumart",
  manifest: "/manifest.webmanifest",
  appleWebApp: { capable: true, title: "Edumart", statusBarStyle: "default" },
  icons: { icon: "/icon.svg", apple: "/icon-maskable.svg" },
  openGraph: {
    title: "Edumart — exam preparation that respects your time",
    description: "Practice, mocks and progress tracking for JAMB, WAEC and NECO.",
    type: "website",
  },
  robots: { index: true, follow: true },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  viewportFit: "cover",
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#f8f7f4" },
    { media: "(prefers-color-scheme: dark)", color: "#06301f" },
  ],
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en-NG">
      <body>
        <a href="#main" className="skip-link">
          Skip to main content
        </a>
        {/* The app is installable as a PWA: the service worker caches the shell, never API state. */}
        <ServiceWorkerRegistration />
        {children}
      </body>
    </html>
  );
}
