import { useEffect, useState } from "react";
import { ScrollView, Text, View } from "react-native";

import { formatPercent, type LearningSummary } from "@edumart/shared";

import { api } from "@/api";
import { Banner, Card, Screen } from "@/components/ui";
import { fetchConsistency, type PhoneConsistency } from "@/live";
import { theme } from "@/theme";

export default function ProgressTab() {
  const [summary, setSummary] = useState<LearningSummary | null>(null);
  const [consistency, setConsistency] = useState<PhoneConsistency | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const { summary: data } = await api.learningSummary();
        setSummary(data);
      } catch {
        setError("We couldn't load your progress.");
      }
      try {
        setConsistency(await fetchConsistency());
      } catch {
        // The streak is supplementary: a failed read must not hide the analytics that did load.
      }
    })();
  }, []);

  return (
    <ScrollView>
      <Screen title="Progress" subtitle="Derived from your submitted answers.">
        {error ? <Banner tone="error">{error}</Banner> : null}
        {consistency ? (
          <Card>
            <Text style={{ fontSize: 12, color: theme.colors.inkMuted }}>Consistency</Text>
            <Text style={{ fontSize: 18, fontWeight: "700" }}>
              {consistency.currentStreak} day{consistency.currentStreak === 1 ? "" : "s"} in a row
            </Text>
            <Text style={{ fontSize: 12, color: theme.colors.inkMuted }}>
              longest {consistency.longestStreak} · {consistency.activeDays} of the last {consistency.windowDays}{" "}
              days active · counted in {consistency.timeZone}
            </Text>
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 3, marginTop: theme.spacing.sm }}>
              {consistency.days.map((day) => (
                <View
                  key={day.date}
                  style={{
                    width: 8,
                    height: 8,
                    borderRadius: 2,
                    backgroundColor: day.active ? theme.colors.primary : theme.colors.border,
                    borderWidth: day.isToday ? 1 : 0,
                    borderColor: theme.colors.ink,
                  }}
                />
              ))}
            </View>
            {consistency.streakAtRisk ? (
              <Text style={{ fontSize: 12, color: theme.colors.ink }}>
                Today is still open — a short practice set keeps the run going.
              </Text>
            ) : null}
          </Card>
        ) : null}
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
