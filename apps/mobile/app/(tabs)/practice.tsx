import { router } from "expo-router";
import { useEffect, useState } from "react";
import { ScrollView, Text } from "react-native";

import { ApiError, type Subject } from "@edumart/shared";

import { api } from "@/api";
import { Banner, Button, Card, Screen } from "@/components/ui";
import { theme } from "@/theme";

export default function PracticeTab() {
  const [subjects, setSubjects] = useState<Subject[]>([]);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const { subjects: list } = await api.subjects();
        setSubjects(list.filter((subject) => subject.questionCount > 0));
      } catch {
        setError("We couldn't load subjects. Check your connection.");
      }
    })();
  }, []);

  async function start(subject: Subject) {
    setBusy(subject.id);
    setError(null);
    try {
      const { session } = await api.startPractice({ subjectId: subject.id, count: 10 });
      router.push(`/attempt/${session.session_id}`);
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "We couldn't start that set.");
    } finally {
      setBusy(null);
    }
  }

  return (
    <ScrollView>
      <Screen title="Practice" subtitle="Ten questions. Answers save as you tap them.">
        {error ? <Banner tone="error">{error}</Banner> : null}
        {subjects.map((subject) => (
          <Card key={subject.id}>
            <Text style={{ fontWeight: "700", color: theme.colors.ink }}>
              {subject.icon} {subject.name}
            </Text>
            <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>{subject.questionCount} reviewed questions</Text>
            <Button label="Start 10 questions" onPress={() => void start(subject)} loading={busy === subject.id} />
          </Card>
        ))}
      </Screen>
    </ScrollView>
  );
}
