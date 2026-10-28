import { useEffect, useState } from "react";
import { ScrollView, Text, View } from "react-native";

import { formatPercent, type LearningSummary } from "@edumart/shared";

import { api } from "@/api";
import { Banner, Card, Screen } from "@/components/ui";
import { theme } from "@/theme";

export default function ProgressTab() {
  const [summary, setSummary] = useState<LearningSummary | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const { summary: data } = await api.learningSummary();
        setSummary(data);
      } catch {
        setError("We couldn't load your progress.");
      }
    })();
  }, []);

  return (
    <ScrollView>
      <Screen title="Progress" subtitle="Derived from your submitted answers.">
        {error ? <Banner tone="error">{error}</Banner> : null}
        {summary ? (
          <>
            <View style={{ flexDirection: "row", gap: theme.spacing.md }}>
              <Card>
                <Text style={{ fontSize: 12, color: theme.colors.inkMuted }}>Attempts</Text>
                <Text style={{ fontSize: 18, fontWeight: "700" }}>{summary.totalAttempts}</Text>
              </Card>
              <Card>
                <Text style={{ fontSize: 12, color: theme.colors.inkMuted }}>Average</Text>
                <Text style={{ fontSize: 18, fontWeight: "700" }}>{formatPercent(summary.averagePercent)}</Text>
              </Card>
            </View>

            {summary.bySubject.map((subject) => (
              <Card key={subject.subjectId}>
                <Text style={{ fontWeight: "600" }}>{subject.subjectName}</Text>
                <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>
                  {formatPercent(subject.accuracy)} accuracy · {subject.attempts} answers
                </Text>
              </Card>
            ))}

            <Card>
              <Text style={{ fontWeight: "700" }}>Topic mastery</Text>
              {summary.topics.length === 0 ? (
                <Text style={{ color: theme.colors.inkMuted, fontSize: 13 }}>Answer a practice set to see topics.</Text>
              ) : (
                summary.topics.map((topic) => (
                  <Text key={topic.topicId} style={{ fontSize: 13, color: theme.colors.ink }}>
                    {topic.topicName} · {topic.accuracy}% ({topic.trend})
                  </Text>
                ))
              )}
            </Card>
          </>
        ) : null}
      </Screen>
    </ScrollView>
  );
}
