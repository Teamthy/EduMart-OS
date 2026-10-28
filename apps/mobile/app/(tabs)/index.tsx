import { Link, router } from "expo-router";
import { useCallback, useEffect, useState } from "react";
import { RefreshControl, ScrollView, Text, View } from "react-native";

import { formatPercent, type Dashboard } from "@edumart/shared";

import { api } from "@/api";
import { useAuth } from "@/auth";
import { Banner, Button, Card, Screen } from "@/components/ui";
import { theme } from "@/theme";

export default function HomeTab() {
  const { user, signOut } = useAuth();
  const [dashboard, setDashboard] = useState<Dashboard | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  const load = useCallback(async () => {
    try {
      const { dashboard: data } = await api.dashboard();
      setDashboard(data);
      setError(null);
    } catch {
      setError("We couldn't refresh your dashboard. Pull down to try again.");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function refresh() {
    setRefreshing(true);
    await load();
    setRefreshing(false);
  }

  return (
    <ScrollView refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}>
      <Screen
        title={`Hi${user?.displayName ? `, ${user.displayName.split(" ")[0]}` : ""}`}
        subtitle={dashboard?.examPathway ? `${dashboard.examPathway} · ${dashboard.daysToExam ?? "—"} days to go` : "Your plan"}
      >
        {error ? <Banner tone="error">{error}</Banner> : null}
        {!dashboard && !error ? <Text style={{ color: theme.colors.inkMuted }}>Loading your plan…</Text> : null}

        {dashboard ? (
          <>
            {dashboard.activeSessionId ? (
              <Card>
                <Text style={{ fontWeight: "700", color: theme.colors.ink }}>Attempt in progress</Text>
                <Text style={{ color: theme.colors.inkMuted, fontSize: 13 }}>
                  The server clock is running — resume before the deadline closes it.
                </Text>
                <Button label="Resume attempt" onPress={() => router.push(`/attempt/${dashboard.activeSessionId}`)} />
              </Card>
            ) : null}

            <Card>
              <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>Average score</Text>
              <Text style={{ fontSize: 20, fontWeight: "700", color: theme.colors.ink }}>
                {formatPercent(dashboard.averagePercent)}
              </Text>
              <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>{dashboard.recommendation}</Text>
            </Card>

            <View style={{ flexDirection: "row", gap: theme.spacing.md }}>
              <Card>
                <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>Points</Text>
                <Text style={{ fontSize: 18, fontWeight: "700", color: theme.colors.ink }}>{dashboard.pointsBalance}</Text>
              </Card>
              <Card>
                <Text style={{ color: theme.colors.inkMuted, fontSize: 12 }}>Streak</Text>
                <Text style={{ fontSize: 18, fontWeight: "700", color: theme.colors.ink }}>{dashboard.streakDays}d</Text>
              </Card>
            </View>

            <Button label="Start a practice set" onPress={() => router.push("/(tabs)/practice")} />
          </>
        ) : null}

        <Card>
          <Text style={{ fontWeight: "700", color: theme.colors.ink }}>Recent attempts</Text>
          {(dashboard?.recentAttempts ?? []).length === 0 ? (
            <Text style={{ color: theme.colors.inkMuted, fontSize: 13 }}>Nothing submitted yet.</Text>
          ) : (
            (dashboard?.recentAttempts ?? []).map((attempt) => (
              <Text key={attempt.id} style={{ color: theme.colors.ink }}>
                {attempt.title} · {formatPercent(attempt.scorePercent)}
              </Text>
            ))
          )}
        </Card>

        <Text style={{ textAlign: "center", color: theme.colors.inkMuted, fontSize: 12 }}>
          <Link href="/(tabs)/account" style={{ color: theme.colors.primary }}>
            Account and settings
          </Link>
        </Text>
        <Button label="Sign out" variant="ghost" onPress={() => void signOut()} />
      </Screen>
    </ScrollView>
  );
}
