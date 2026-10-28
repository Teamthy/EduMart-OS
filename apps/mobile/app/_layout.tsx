import { Stack } from "expo-router";
import { StatusBar } from "expo-status-bar";

import { AuthProvider } from "@/auth";
import { theme } from "@/theme";

/**
 * Root navigator. The signup wizard and the signed-in tabs are separate stacks, which keeps the
 * back behaviour of each flow honest (you cannot "back" into the wizard from the app).
 */
export default function RootLayout() {
  return (
    <AuthProvider>
      <StatusBar style="dark" />
      <Stack
        screenOptions={{
          headerStyle: { backgroundColor: theme.colors.background },
          headerShadowVisible: false,
          headerTitleStyle: { fontWeight: "700" },
          headerTintColor: theme.colors.primary,
          contentStyle: { backgroundColor: theme.colors.background },
        }}
      >
        <Stack.Screen name="index" options={{ headerShown: false }} />
        <Stack.Screen name="signin" options={{ title: "Sign in" }} />
        <Stack.Screen name="signup/pathway" options={{ title: "Create account" }} />
        <Stack.Screen name="signup/subjects" options={{ title: "Subjects" }} />
        <Stack.Screen name="signup/schedule" options={{ title: "Your schedule" }} />
        <Stack.Screen name="signup/account" options={{ title: "Your details" }} />
        <Stack.Screen name="signup/ready" options={{ title: "You're set", headerBackVisible: false }} />
        <Stack.Screen name="(tabs)" options={{ headerShown: false }} />
        <Stack.Screen name="attempt/[id]" options={{ title: "Attempt", headerBackVisible: false }} />
        <Stack.Screen name="results/[id]" options={{ title: "Result" }} />
      </Stack>
    </AuthProvider>
  );
}
