import { router } from "expo-router";
import { useEffect, useState } from "react";
import { ScrollView, Text } from "react-native";

import { SIGNUP_STEPS, daysUntil, studyLoadAdvice } from "@edumart/shared";

import { signupApi } from "@/api";
import { Banner, Button, Choice, Field, Screen, StepHeader } from "@/components/ui";
import { theme } from "@/theme";

const DEFAULT_DATE = () => {
  const target = new Date();
  target.setDate(target.getDate() + 60);
  return target.toISOString().slice(0, 10);
};

export default function ScheduleStep() {
  const [targetDate, setTargetDate] = useState(DEFAULT_DATE);
  const [dailyMinutes, setDailyMinutes] = useState(30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      const current = await signupApi.current();
      if (current.draft?.targetDate) setTargetDate(current.draft.targetDate);
      if (current.draft?.dailyMinutes) setDailyMinutes(current.draft.dailyMinutes);
    })();
  }, []);

  async function next() {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(targetDate)) {
      setError("Enter your exam date as YYYY-MM-DD.");
      return;
    }
    if (dailyMinutes < 10 || dailyMinutes > 240) {
      setError("Choose between 10 and 240 minutes a day.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await signupApi.patch({ currentStep: "account", targetDate, dailyMinutes });
      router.push("/signup/account");
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not save this step.");
    } finally {
      setBusy(false);
    }
  }

  const step = SIGNUP_STEPS[2]!;
  const remaining = daysUntil(targetDate);

  return (
    <ScrollView contentContainerStyle={{ flexGrow: 1 }}>
      <Screen title={step.title} subtitle={step.description}>
        <StepHeader step={3} total={SIGNUP_STEPS.length} label={step.label} />
        {error ? <Banner tone="error">{error}</Banner> : null}

        <Field
          label="Exam date (YYYY-MM-DD)"
          value={targetDate}
          onChangeText={setTargetDate}
          keyboardType="numeric"
          hint={remaining !== null ? `${remaining} days to prepare` : undefined}
        />

        {[15, 30, 45, 60, 90, 120].map((minutes) => (
          <Choice
            key={minutes}
            label={minutes >= 60 ? `${minutes / 60} hour${minutes === 60 ? "" : "s"} a day` : `${minutes} minutes a day`}
            selected={dailyMinutes === minutes}
            onPress={() => setDailyMinutes(minutes)}
          />
        ))}
        <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>{studyLoadAdvice(dailyMinutes)}</Text>

        <Button label="Continue" onPress={next} loading={busy} />
        <Button label="Back" variant="ghost" onPress={() => router.back()} />
      </Screen>
    </ScrollView>
  );
}
