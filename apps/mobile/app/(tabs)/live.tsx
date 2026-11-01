import { useCallback, useEffect, useState } from "react";
import { Linking, ScrollView, Text } from "react-native";

import { formatDateTimeSafe } from "@edumart/shared";

import { Banner, Button, Card, Screen } from "@/components/ui";
import { fetchExams, fetchLiveClasses, type PhoneExam } from "@/live";
import { theme } from "@/theme";

interface LiveClass {
  id: string;
  slug: string;
  title: string;
  creditsCost: number;
  capacity: number;
  durationMinutes: number;
  status: string;
  startsAt: string;
  attendees: number;
  hostName: string;
  role: string;
  joinUrl: string;
}

/**
 * Live classes on the phone.
 *
 * The room itself opens in the browser rather than inside the app: a forwarded WebRTC session plus a canvas
 * whiteboard is the web app's job, and two implementations of the same room would disagree about the board's key
 * and about presence. What the phone is genuinely good at is the part around it — finding a class or a sitting,
 * having the link ready, and knowing whether the exam is open — so that is what this screen owns.
 */
export default function LiveTab() {
  const [classes, setClasses] = useState<LiveClass[] | null>(null);
  const [exams, setExams] = useState<PhoneExam[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const [liveClasses, sittings] = await Promise.all([fetchLiveClasses(), fetchExams()]);
      setClasses(liveClasses);
      setExams(sittings);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Check your connection and try again.");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <ScrollView>
      <Screen title="Live & exams" subtitle="Rooms and sittings you host or are invited to. A room opens in the browser, where the video and whiteboard live.">
        {error ? <Banner tone="error">{error}</Banner> : null}

        {classes === null && !error ? (
          <Card>
            <Text style={{ color: theme.colors.inkMuted }}>Loading…</Text>
          </Card>
        ) : null}

        {classes && classes.length === 0 ? (
          <Card>
            <Text style={{ color: theme.colors.inkMuted, fontSize: 13 }}>
              No live classes yet. Schedule one on the web app and it appears here with its join link.
            </Text>
          </Card>
        ) : null}

        {classes?.map((item) => (
          <Card key={item.id}>
            <Text style={{ fontSize: 16, fontWeight: "700" }}>{item.title}</Text>
            <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>
              {formatDateTimeSafe(item.startsAt)} · {item.attendees} seated · host {item.hostName}
            </Text>
            <Text style={{ color: theme.colors.ink, fontSize: 13 }}>
              {item.status}
              {item.creditsCost > 0 ? ` · ${item.creditsCost} credits` : " · free · 45 min"}
              {` · ${item.attendees}/${item.capacity} seats`}
              {item.role === "host" ? " · you host" : ""}
            </Text>
            <Button label="Open room" onPress={() => void Linking.openURL(item.joinUrl)} />
            <Button
              label="Copy link elsewhere"
              variant="ghost"
              onPress={() => void Linking.openURL(`mailto:?subject=${encodeURIComponent(item.title)}&body=${encodeURIComponent(item.joinUrl)}`)}
            />
          </Card>
        ))}

        <Text style={{ fontSize: 16, fontWeight: "700", marginTop: theme.spacing.md }}>Synchronised exams</Text>
        <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>
          One paper, one window. Open a sitting in the browser to answer it; the deadline and the score are the
          server&apos;s.
        </Text>

        {exams && exams.length === 0 ? (
          <Card>
            <Text style={{ color: theme.colors.inkMuted, fontSize: 13 }}>
              No sittings yet. Scheduling one on the web app gives you a link you can share here.
            </Text>
          </Card>
        ) : null}

        {exams?.map((exam) => (
          <Card key={exam.id}>
            <Text style={{ fontSize: 16, fontWeight: "700" }}>{exam.title}</Text>
            <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>
              {formatDateTimeSafe(exam.startsAt)} · {exam.durationMinutes} min · {exam.questionCount} questions ·{" "}
              {exam.candidates} sat
            </Text>
            <Text style={{ color: theme.colors.ink, fontSize: 13 }}>
              {exam.open ? "open now" : "not open"}
              {exam.shuffled ? " · shuffled per candidate" : ""}
              {exam.role === "host" ? " · you host" : ""}
              {exam.myAttempt && exam.myAttempt.status === "in_progress"
                ? ` · ${Math.ceil(exam.myAttempt.remainingSeconds / 60)} min left in your attempt`
                : exam.myAttempt?.scorePercent !== null && exam.myAttempt?.scorePercent !== undefined
                  ? ` · scored ${exam.myAttempt.scorePercent}%`
                  : ""}
            </Text>
            <Button
              label={exam.role === "host" ? "Open report" : exam.open ? "Sit this exam" : "See the sitting"}
              onPress={() => void Linking.openURL(exam.url)}
            />
          </Card>
        ))}

        <Button label="Refresh" variant="secondary" onPress={() => void load()} />
      </Screen>
    </ScrollView>
  );
}
