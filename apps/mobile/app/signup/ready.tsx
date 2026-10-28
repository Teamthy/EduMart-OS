import { router } from "expo-router";
import { Text } from "react-native";

import { SIGNUP_STEPS, daysUntil, formatDate } from "@edumart/shared";

import { useAuth } from "@/auth";
import { Banner, Button, Card, Screen, StepHeader } from "@/components/ui";
import { theme } from "@/theme";

export default function ReadyStep() {
  const { user } = useAuth();
  const step = SIGNUP_STEPS[4]!;
  const days = daysUntil(user?.targetDate ?? null);

  return (
    <Screen title={`${step.title}, ${user?.displayName.split(" ")[0] ?? "friend"}`} subtitle={step.description}>
      <StepHeader step={5} total={SIGNUP_STEPS.length} label={step.label} />
      <Banner tone="success">Your plan is saved on our server and will sync to the web app.</Banner>

      <Card>
        <Text style={{ fontWeight: "700", color: theme.colors.ink }}>Exam date</Text>
        <Text style={{ color: theme.colors.inkMuted }}>
          {formatDate(user?.targetDate ?? null)}
          {days !== null ? ` · ${days} days to prepare` : ""}
        </Text>
        <Text style={{ fontWeight: "700", color: theme.colors.ink, marginTop: 8 }}>Daily commitment</Text>
        <Text style={{ color: theme.colors.inkMuted }}>{user?.dailyMinutes ?? 30} minutes</Text>
        <Text style={{ fontWeight: "700", color: theme.colors.ink, marginTop: 8 }}>Subjects</Text>
        <Text style={{ color: theme.colors.inkMuted }}>{user?.subjectIds.length ?? 0} selected</Text>
      </Card>

      <Button label="Start practising" onPress={() => router.replace("/(tabs)/practice")} />
      <Button label="Go to home" variant="secondary" onPress={() => router.replace("/(tabs)")} />
    </Screen>
  );
}
