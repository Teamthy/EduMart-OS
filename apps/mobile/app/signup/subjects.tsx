import { router } from "expo-router";
import { useEffect, useState } from "react";
import { ScrollView, Text } from "react-native";

import { SIGNUP_STEPS, type Subject } from "@edumart/shared";

import { api, signupApi } from "@/api";
import { Banner, Button, Choice, Screen, StepHeader } from "@/components/ui";
import { theme } from "@/theme";

export default function SubjectsStep() {
  const [subjects, setSubjects] = useState<Subject[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const current = await signupApi.current();
        const pathwayKey = current.draft?.examPathwayKey;
        const { subjects: list } = await api.subjects(pathwayKey || undefined);
        setSubjects(list.length > 0 ? list : (await api.subjects()).subjects);
        setSelected(current.draft?.subjectIds ?? []);
      } catch {
        setError("We couldn't load subjects. Check your connection.");
      }
    })();
  }, []);

  function toggle(id: string) {
    setSelected((current) => (current.includes(id) ? current.filter((item) => item !== id) : [...current, id]));
  }

  async function next() {
    if (selected.length === 0) {
      setError("Pick at least one subject.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await signupApi.patch({ currentStep: "schedule", subjectIds: selected });
      router.push("/signup/schedule");
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not save this step.");
    } finally {
      setBusy(false);
    }
  }

  const step = SIGNUP_STEPS[1]!;

  return (
    <ScrollView contentContainerStyle={{ flexGrow: 1 }}>
      <Screen title={step.title} subtitle={step.description}>
        <StepHeader step={2} total={SIGNUP_STEPS.length} label={step.label} />
        {error ? <Banner tone="error">{error}</Banner> : null}
        {subjects.map((subject) => (
          <Choice
            key={subject.id}
            accessibilityRole="checkbox"
            label={`${subject.icon} ${subject.name}`}
            detail={`${subject.questionCount} reviewed questions`}
            selected={selected.includes(subject.id)}
            onPress={() => toggle(subject.id)}
          />
        ))}
        <Button label={`Continue (${selected.length} selected)`} onPress={next} loading={busy} disabled={selected.length === 0} />
        <Button label="Back" variant="ghost" onPress={() => router.back()} />
        <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>You can change these later in Account.</Text>
      </Screen>
    </ScrollView>
  );
}
