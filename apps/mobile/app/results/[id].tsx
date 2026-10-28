import { router, useLocalSearchParams } from "expo-router";
import { useEffect, useState } from "react";
import { ScrollView, Text, View } from "react-native";

import { formatPercent, type AssessmentSessionView } from "@edumart/shared";

import { api } from "@/api";
import { Banner, Button, Card, Screen } from "@/components/ui";
import { theme } from "@/theme";

export default function ResultScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const [session, setSession] = useState<AssessmentSessionView | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const { session: loaded } = await api.session(id);
        if (loaded.status === "in_progress") {
          router.replace(`/attempt/${loaded.session_id}`);
          return;
        }
        setSession(loaded);
      } catch {
        setError("We couldn't load this result.");
      }
    })();
  }, [id]);

  if (error) {
    return (
      <Screen title="Result unavailable">
        <Banner tone="error">{error}</Banner>
        <Button label="Back home" onPress={() => router.replace("/(tabs)")} />
      </Screen>
    );
  }

  if (!session) {
    return (
      <Screen title="Loading result…">
        <Text style={{ color: theme.colors.inkMuted }}>Fetching your score from the server.</Text>
      </Screen>
    );
  }

  const keys = new Map((session.review ?? []).map((item) => [item.qvId, item]));

  return (
    <ScrollView>
      <Screen
        title={session.status === "expired" ? "Closed at the deadline" : "Submitted"}
        subtitle={session.mode === "practice" ? "Practice result" : "Assessment result"}
      >
        {session.score ? (
          <Card>
            <Text style={{ fontSize: 34, fontWeight: "700", color: theme.colors.primary }}>
              {formatPercent(session.score.percent)}
            </Text>
            <Text style={{ color: theme.colors.inkMuted }}>
              {session.score.raw} of {session.score.max} marks across {session.question_count} questions
            </Text>
          </Card>
        ) : (
          <Banner tone="error">This attempt closed before any answers were saved.</Banner>
        )}

        {!session.result_released ? (
          <Banner tone="info">Answers are released later. Your score is final.</Banner>
        ) : null}

        {session.result_released
          ? session.questions.map((question) => {
              const key = keys.get(question.qvId);
              const correct = key && question.selectedOptionId === key.answerOptionId;
              return (
                <Card key={question.qvId}>
                  <Text style={{ fontWeight: "700", color: theme.colors.ink }}>
                    Q{question.position}. {question.stem}
                  </Text>
                  <Text style={{ color: correct ? theme.colors.success : question.selectedOptionId ? theme.colors.danger : theme.colors.inkMuted, fontSize: 12 }}>
                    {correct ? "Correct" : question.selectedOptionId ? "Incorrect" : "Unanswered"}
                  </Text>
                  <View style={{ gap: 4, marginTop: 6 }}>
                    {question.options.map((option) => {
                      const isAnswer = key?.answerOptionId === option.id;
                      const isChoice = question.selectedOptionId === option.id;
                      return (
                        <Text
                          key={option.id}
                          style={{
                            fontSize: 13,
                            color: theme.colors.ink,
                            backgroundColor: isAnswer
                              ? theme.colors.successSoft
                              : isChoice
                                ? theme.colors.dangerSoft
                                : "transparent",
                            padding: 4,
                            borderRadius: 6,
                          }}
                        >
                          {option.label}
                          {isAnswer ? "  ✓ correct" : isChoice ? "  · your answer" : ""}
                        </Text>
                      );
                    })}
                  </View>
                  {key?.explanation ? (
                    <Text style={{ fontSize: 13, color: theme.colors.inkMuted }}>Why: {key.explanation}</Text>
                  ) : null}
                </Card>
              );
            })
          : null}

        <Button label="Practise again" onPress={() => router.replace("/(tabs)/practice")} />
        <Button label="Back home" variant="secondary" onPress={() => router.replace("/(tabs)")} />
      </Screen>
    </ScrollView>
  );
}
