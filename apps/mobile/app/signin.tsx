import { Link, router } from "expo-router";
import { useState } from "react";
import { ScrollView, Text } from "react-native";

import { ApiError } from "@edumart/shared";

import { useAuth } from "@/auth";
import { Banner, Button, Field, Screen } from "@/components/ui";
import { theme } from "@/theme";

export default function SignInScreen() {
  const { signIn } = useAuth();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      await signIn(email.trim(), password);
      router.replace("/(tabs)");
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "We couldn't sign you in.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <ScrollView contentContainerStyle={{ padding: theme.spacing.lg, gap: theme.spacing.md }}>
      <Screen title="Welcome back" subtitle="Your progress and streak are where you left them.">
        {error ? <Banner tone="error">{error}</Banner> : null}

        <Field
          label="Email"
          value={email}
          onChangeText={setEmail}
          keyboardType="email-address"
          autoCapitalize="none"
          placeholder="you@example.com"
        />
        <Field label="Password" value={password} onChangeText={setPassword} secureTextEntry />

        <Button label="Sign in" onPress={submit} loading={busy} disabled={!email || !password} />

        <Text style={{ textAlign: "center", color: theme.colors.inkMuted, fontSize: 13 }}>
          New here?{" "}
          <Link href="/signup/pathway" style={{ color: theme.colors.primary, fontWeight: "700" }}>
            Start with 5 quick steps
          </Link>
        </Text>
      </Screen>
    </ScrollView>
  );
}
