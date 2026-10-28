import { router } from "expo-router";
import { useEffect, useState } from "react";
import { ScrollView, Text } from "react-native";

import { ApiError, SIGNUP_STEPS, validateStep, type SignupDraft } from "@edumart/shared";

import { api, draftStore, signupApi, tokenStore } from "@/api";
import { useAuth } from "@/auth";
import { Banner, Button, Field, Screen, StepHeader } from "@/components/ui";
import { theme } from "@/theme";

export default function AccountStep() {
  const { refresh } = useAuth();
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [referralCode, setReferralCode] = useState("");
  const [draft, setDraft] = useState<Awaited<ReturnType<typeof signupApi.current>> | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void (async () => {
      const current = await signupApi.current();
      setDraft(current);
      setDisplayName(current.draft?.displayName ?? "");
      setEmail(current.draft?.email ?? "");
      setReferralCode(current.referralCode ?? "");
    })();
  }, []);

  async function submit() {
    const form: SignupDraft = {
      examPathwayKey: draft?.draft?.examPathwayKey ?? "",
      examPathwayId: draft?.draft?.examPathwayId ?? "",
      subjectIds: draft?.draft?.subjectIds ?? [],
      targetDate: draft?.draft?.targetDate ?? "",
      dailyMinutes: draft?.draft?.dailyMinutes ?? 30,
      displayName,
      email,
      password,
      referralCode,
      timezone: "Africa/Lagos",
    };

    const validation = validateStep("account", form);
    if (Object.keys(validation).length > 0) {
      setErrors(validation as Record<string, string>);
      return;
    }

    setBusy(true);
    setErrors({});
    setError(null);
    try {
      // Persist the visible fields first, then create the account from the same draft.
      await signupApi.patch({ currentStep: "confirm", displayName, email, referralCode });
      const session = await api.register({
        displayName: displayName.trim(),
        email: email.trim().toLowerCase(),
        password,
        examPathwayId: form.examPathwayId || null,
        targetDate: form.targetDate || null,
        subjectIds: form.subjectIds,
        dailyMinutes: form.dailyMinutes,
        referralCode: referralCode.trim().toUpperCase(),
        timezone: form.timezone,
        client: "mobile",
        onboardingStep: SIGNUP_STEPS.length,
      });

      await tokenStore.set(session.token);
      await draftStore.clear();
      await refresh();
      router.replace("/signup/ready");
    } catch (caught) {
      if (caught instanceof ApiError) {
        const fieldErrors: Record<string, string> = {};
        for (const detail of caught.fieldErrors) fieldErrors[detail.field] = detail.message;
        setErrors(fieldErrors);
        setError(caught.message);
      } else {
        setError("We couldn't create your account. Please try again.");
      }
    } finally {
      setBusy(false);
    }
  }

  const step = SIGNUP_STEPS[3]!;

  return (
    <ScrollView contentContainerStyle={{ flexGrow: 1 }}>
      <Screen title={step.title} subtitle={step.description}>
        <StepHeader step={4} total={SIGNUP_STEPS.length} label={step.label} />
        {error ? <Banner tone="error">{error}</Banner> : null}

        <Field label="Full name" value={displayName} onChangeText={setDisplayName} autoCapitalize="words" error={errors.displayName} />
        <Field
          label="Email"
          value={email}
          onChangeText={setEmail}
          keyboardType="email-address"
          autoCapitalize="none"
          error={errors.email}
        />
        <Field
          label="Password"
          value={password}
          onChangeText={setPassword}
          secureTextEntry
          error={errors.password}
          hint="At least 8 characters, with a letter and a number."
        />
        <Field
          label="Referral code (optional)"
          value={referralCode}
          onChangeText={setReferralCode}
          autoCapitalize="characters"
          placeholder="AMARA-2026"
        />

        <Button label="Create account" onPress={submit} loading={busy} />
        <Button label="Back" variant="ghost" onPress={() => router.back()} />
        <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>
          By continuing you agree to our terms and privacy notice.
        </Text>
      </Screen>
    </ScrollView>
  );
}
