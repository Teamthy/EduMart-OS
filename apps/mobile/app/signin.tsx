import { Link, router } from "expo-router";
import { useState } from "react";
import { ScrollView, Text } from "react-native";

import { ApiError } from "@edumart/shared";

import { useAuth } from "@/auth";
import { Banner, Button, Field, Screen } from "@/components/ui";
import { theme } from "@/theme";

export default function SignInScreen() {
  const { signIn, completeSignIn } = useAuth();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Set when the password was right and a code is owed. It holds the challenge token, which grants nothing on its
  // own — it is the sign-in, not the account.
  const [challenge, setChallenge] = useState<string | null>(null);
  const [code, setCode] = useState("");

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      const outcome = await signIn(email.trim(), password);
      if (outcome.twoFactor) {
        // Not a failure: the password was accepted. Clear it so it is not sitting in state during the second step.
        setChallenge(outcome.token);
        setPassword("");
        return;
      }
      router.replace("/(tabs)");
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "We couldn't sign you in.");
    } finally {
      setBusy(false);
    }
  }

  async function submitCode() {
    if (!challenge) return;
    setBusy(true);
    setError(null);
    try {
      await completeSignIn(challenge, code);
      router.replace("/(tabs)");
    } catch (caught) {
      const apiError = caught instanceof ApiError ? caught : null;
      setError(apiError?.message ?? "That code did not work.");
      setCode("");
      // An expired challenge cannot be retried; go back to the password step instead of typing into a dead token.
      if (apiError?.code === "TWO_FACTOR_REQUIRED") setChallenge(null);
    } finally {
      setBusy(false);
    }
  }

  if (challenge) {
    return (
      <ScrollView contentContainerStyle={{ padding: theme.spacing.lg, gap: theme.spacing.md }}>
        <Screen title="Two-step verification" subtitle="Your password was accepted. Enter the six-digit code from your authenticator app.">
          {error ? <Banner tone="error">{error}</Banner> : null}

          <Field
            label="Verification code"
            value={code}
            onChangeText={setCode}
            keyboardType="number-pad"
            autoComplete="one-time-code"
            placeholder="123456"
          />

          <Button label="Verify and sign in" onPress={submitCode} loading={busy} disabled={code.trim().length < 6} />

          <Text style={{ textAlign: "center", color: theme.colors.inkMuted, fontSize: 13 }}>
            Lost your phone? Enter one of the recovery codes you saved when you turned this on.
          </Text>
          <Text
            style={{ textAlign: "center", color: theme.colors.primary, fontWeight: "700", fontSize: 13 }}
            onPress={() => {
              setChallenge(null);
              setCode("");
              setError(null);
            }}
          >
            Use a different account
          </Text>
        </Screen>
      </ScrollView>
    );
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
