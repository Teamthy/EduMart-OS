import type { Metadata, Viewport } from "next";
import "./globals.css";
import { ServiceWorkerRegister } from "./sw-register";
export const metadata: Metadata = { title: "Edumart", description: "Learn, practice, and prepare with Edumart", applicationName: "Edumart", manifest: "/manifest.json" };
export const viewport: Viewport = { themeColor: "#142b5f", width: "device-width", initialScale: 1 };
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) { return <html lang="en"><body>{children}<ServiceWorkerRegister /></body></html>; }
