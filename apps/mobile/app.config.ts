import type { ExpoConfig } from "expo/config";

/**
 * Expo configuration for the Edumart app.
 *
 * The app ships alongside the PWA from the same commit: identical API contract (`packages/shared`),
 * identical design tokens, identical signup steps. Only the shell differs.
 */
const config: ExpoConfig = {
  name: "Edumart",
  slug: "edumart",
  version: "1.0.0",
  orientation: "portrait",
  scheme: "edumart",
  userInterfaceStyle: "light",
  newArchEnabled: true,
  icon: "./assets/icon.png",
  splash: {
    image: "./assets/splash.png",
    resizeMode: "contain",
    backgroundColor: "#f8f7f4",
  },
  assetBundlePatterns: ["**/*"],
  ios: {
    supportsTablet: true,
    bundleIdentifier: "ng.edumart.app",
    infoPlist: {
      // No tracking, no location: the app collects the minimum needed to run assessments.
      NSPhotoLibraryUsageDescription: "Edumart does not access your photos.",
    },
  },
  android: {
    package: "ng.edumart.app",
    versionCode: 1,
    adaptiveIcon: {
      foregroundImage: "./assets/adaptive-icon.png",
      backgroundColor: "#0d5c46",
    },
    permissions: ["INTERNET"],
  },
  web: { bundler: "metro", output: "static", favicon: "./assets/favicon.png" },
  plugins: ["expo-router", "expo-secure-store"],
  experiments: { typedRoutes: true },
  extra: {
    // Overridable per build profile; the app never hard-codes a production host in source.
    apiUrl: process.env.EXPO_PUBLIC_API_URL ?? "http://localhost:8080",
    eas: { projectId: process.env.EAS_PROJECT_ID ?? "" },
  },
  runtimeVersion: { policy: "appVersion" },
  updates: { fallbackToCacheTimeout: 0 },
};

export default config;
