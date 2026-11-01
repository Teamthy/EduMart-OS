import { router } from "expo-router";
import { useEffect, useState } from "react";
import { ScrollView, Text } from "react-native";

import { SIGNUP_STEPS, validateStep, type Pathway, type SignupDraft } from "@edumart/shared";

import { api, signupApi } from "@/api";
import { Banner, Button, Choice, Screen, StepHeader } from "@/components/ui";
import { theme } from "@/theme";

/**
 * Step 1 of 5. The answer is written to the server draft, so closing the app here and reopening it
 * on the web resumes at step 2 with the same choice.
 */
export default function PathwayStep() {
  const [pathways, setPathways] = useState<Pathway[]>([]);
  const [selected, setSelected] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const { pathways: list } = await api.pathways();
        setPathways(list);
        const current = await signupApi.current();
        if (current.draft?.examPathwayId) setSelected(current.draft.examPathwayId);
      } catch {
        setError("We couldn't load the exam list. Check your connection.");
      }
    })();
  }, []);

  async function next() {
    const draft: SignupDraft = {
      // Pathway comes before the account step, which asks what kind of account this is.
      accountType: "student",
      examPathwayKey: "",
      examPathwayId: selected,
      subjectIds: [],
      targetDate: "",
      dailyMinutes: 30,
      displayName: "",
      email: "",
      password: "",
      referralCode: "",
      timezone: "Africa/Lagos",
    };
    const errors = validateStep("pathway", draft);
    if (Object.keys(errors).length > 0) {
      setError(errors.examPathwayId ?? "Choose an exam.");
      return;
    }

    setBusy(true);
    setError(null);
    try {
      const chosen = pathways.find((pathway) => pathway.id === selected);
      await signupApi.patch({ currentStep: "subjects", examPathwayId: selected, examPathwayKey: chosen?.key ?? "" });
      router.push("/signup/subjects");
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not save this step.");
    } finally {
      setBusy(false);
    }
  }

  const step = SIGNUP_STEPS[0]!;

  return (
    <ScrollView contentContainerStyle={{ flexGrow: 1 }}>
      <Screen title={step.title} subtitle={step.description}>
        <StepHeader step={1} total={SIGNUP_STEPS.length} label={step.label} />
        {error ? <Banner tone="error">{error}</Banner> : null}
        {pathways.map((pathway) => (
          <Choice
            key={pathway.id}
            label={pathway.name}
            detail={`${pathway.blurb || pathway.body} · ${pathway.subjects} subjects`}
            selected={selected === pathway.id}
            onPress={() => setSelected(pathway.id)}
          />
        ))}
        <Button label="Continue" onPress={next} loading={busy} disabled={!selected} />
        <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>
          Progress is saved on the server at every step.
        </Text>
      </Screen>
    </ScrollView>
  );
}
