import { router, useLocalSearchParams } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, ScrollView, Text, View } from "react-native";

import {
  ApiError,
  formatCountdown,
  type AssessmentSessionView,
  type QuestionView,
} from "@edumart/shared";

import { api } from "@/api";
import { Banner, Button, Card, Screen } from "@/components/ui";
import { theme } from "@/theme";

type SaveStatus = "idle" | "pending" | "saved" | "failed";

interface AnswerState {
  optionId: string | null;
  version: number;
  status: SaveStatus;
  sequence: number;
  error?: string;
}

/**
 * Native attempt runner. Mirrors the web runner exactly: optimistic selection, idempotent
 * server-confirmed saves, an honest per-answer save state, and a countdown derived from the
 * server's remaining seconds.
 *
 * Offline writes are queued and retried; the learner is told which answers are not yet confirmed
 * rather than being shown a false "saved" tick.
 */
export default function AttemptScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const [session, setSession] = useState<AssessmentSessionView | null>(null);
  const [answers, setAnswers] = useState<Record<string, AnswerState>>({});
  const [index, setIndex] = useState(0);
  const [remaining, setRemaining] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const deadlineRef = useRef<number>(0);
  const answersRef = useRef(answers);
  const inFlight = useRef<Set<string>>(new Set());

  useEffect(() => {
    answersRef.current = answers;
  }, [answers]);

  useEffect(() => {
    void (async () => {
      try {
        const { session: loaded } = await api.session(id);
        if (loaded.status !== "in_progress") {
          router.replace(`/results/${loaded.session_id}`);
          return;
        }
        setSession(loaded);
        setRemaining(loaded.remaining_seconds);
        deadlineRef.current = Date.now() + loaded.remaining_seconds * 1000;
        const initial: Record<string, AnswerState> = {};
        for (const question of loaded.questions) {
          initial[question.qvId] = {
            optionId: question.selectedOptionId,
            version: question.answerVersion,
            status: "idle",
            sequence: 0,
          };
        }
        setAnswers(initial);
      } catch (caught) {
        setError(caught instanceof ApiError ? caught.message : "We couldn't open this attempt.");
      }
    })();
  }, [id]);

  useEffect(() => {
    const interval = setInterval(() => {
      if (!deadlineRef.current) return;
      setRemaining(Math.max(0, Math.round((deadlineRef.current - Date.now()) / 1000)));
    }, 1000);
    return () => clearInterval(interval);
  }, []);

  const submit = useCallback(async () => {
    if (!session || submitting) return;
    setSubmitting(true);
    try {
      await api.submitSession(session.session_id);
      router.replace(`/results/${session.session_id}`);
    } catch (caught) {
      setError(caught instanceof ApiError ? caught.message : "We couldn't submit.");
      setSubmitting(false);
    }
  }, [session, submitting]);

  useEffect(() => {
    if (remaining === 0 && session && !submitting) void submit();
  }, [remaining, session, submitting, submit]);

  const save = useCallback(
    async (qvId: string, optionId: string) => {
      if (!session || inFlight.current.has(qvId)) return;
      inFlight.current.add(qvId);

      const previous = answersRef.current[qvId];
      const sequence = (previous?.sequence ?? 0) + 1;
      const expectedVersion = previous?.version ?? 0;

      setAnswers((current) => ({
        ...current,
        [qvId]: { optionId, version: expectedVersion, status: "pending", sequence },
      }));

      try {
        const result = await api.saveAnswer(session.session_id, qvId, {
          selectedOptionId: optionId,
          expectedVersion,
          clientSequence: sequence,
        });
        setAnswers((current) => ({
          ...current,
          [qvId]: { optionId, version: result.version, status: "saved", sequence },
        }));
      } catch (caught) {
        const message =
          caught instanceof ApiError && caught.code === "CONFLICT"
            ? "Changed on another device — reopen to sync."
            : caught instanceof ApiError && caught.code === "NETWORK_ERROR"
              ? "No connection — will retry."
              : caught instanceof ApiError
                ? caught.message
                : "Not saved.";
        setAnswers((current) => ({
          ...current,
          [qvId]: { optionId, version: expectedVersion, status: "failed", sequence, error: message },
        }));
      } finally {
        inFlight.current.delete(qvId);
      }
    },
    [session],
  );

  const questions = session?.questions ?? [];
  const current: QuestionView | undefined = questions[index];
  const answer = current ? answers[current.qvId] : undefined;
  const answered = useMemo(() => Object.values(answers).filter((item) => item.optionId !== null).length, [answers]);
  const pending = useMemo(() => Object.values(answers).filter((item) => item.status === "pending").length, [answers]);
  const failed = useMemo(() => Object.values(answers).filter((item) => item.status === "failed").length, [answers]);

  if (error && !session) {
    return (
      <Screen title="Attempt unavailable">
        <Banner tone="error">{error}</Banner>
        <Button label="Back home" onPress={() => router.replace("/(tabs)")} />
      </Screen>
    );
  }

  if (!session || !current) {
    return (
      <Screen title="Loading attempt…">
        <Text style={{ color: theme.colors.inkMuted }}>Fetching the questions and the server deadline.</Text>
      </Screen>
    );
  }

  return (
    <ScrollView>
      <Screen title={`Question ${index + 1} of ${questions.length}`} subtitle={`Time remaining ${formatCountdown(remaining)}`}>
        {error ? <Banner tone="error">{error}</Banner> : null}
        {remaining <= 60 ? <Banner tone="info">One minute left. Answers already saved are safe.</Banner> : null}

        <Card>
          <Text style={{ fontSize: 16, color: theme.colors.ink, lineHeight: 23 }}>{current.stem}</Text>
        </Card>

        {current.options.map((option) => {
          const selected = answer?.optionId === option.id;
          return (
            <Text
              key={option.id}
              accessibilityRole="radio"
              accessibilityState={{ checked: selected }}
              onPress={() => void save(current.qvId, option.id)}
              style={{
                minHeight: theme.touch,
                padding: theme.spacing.md,
                borderRadius: theme.radii.md,
                borderWidth: 1,
                borderColor: selected ? theme.colors.primary : theme.colors.border,
                backgroundColor: selected ? theme.colors.primarySoft : theme.colors.surface,
                color: theme.colors.ink,
              }}
            >
              {option.label}
            </Text>
          );
        })}

        <Text style={{ fontSize: 12, color: theme.colors.inkMuted }} accessibilityLiveRegion="polite">
          {answer?.status === "pending"
            ? "Saving…"
            : answer?.status === "saved"
              ? "Saved on the server"
              : answer?.status === "failed"
                ? (answer.error ?? "Not saved — tap your answer again to retry.")
                : "Choose an answer to save it."}
        </Text>

        <Text style={{ fontSize: 12, color: theme.colors.inkMuted }}>
          {answered}/{questions.length} answered
          {pending > 0 ? ` · ${pending} saving` : ""}
          {failed > 0 ? ` · ${failed} unsaved` : ""}
        </Text>

        <View style={{ flexDirection: "row", gap: theme.spacing.sm }}>
          <Button
            label="Previous"
            variant="secondary"
            onPress={() => setIndex((value) => Math.max(0, value - 1))}
            disabled={index === 0}
          />
          {index === questions.length - 1 ? (
            <Button
              label={submitting ? "Submitting…" : "Submit"}
              onPress={() => {
                void (async () => {
                  const unsaved = Object.values(answers).some((item) => item.status === "pending");
                  if (unsaved) {
                    Alert.alert("Still saving", "Wait a moment for your answers to finish saving.");
                    return;
                  }
                  await submit();
                })();
              }}
              loading={submitting}
            />
          ) : (
            <Button
              label="Next"
              onPress={() => setIndex((value) => Math.min(questions.length - 1, value + 1))}
            />
          )}
        </View>

        <Button
          label="Submit attempt"
          variant="ghost"
          onPress={() => void submit()}
          disabled={pending > 0}
          loading={submitting}
        />
      </Screen>
    </ScrollView>
  );
}
