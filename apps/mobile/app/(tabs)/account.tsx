import { router } from "expo-router";
import { Text } from "react-native";

import { formatDate } from "@edumart/shared";

import { useAuth } from "@/auth";
import { Button, Card, Screen } from "@/components/ui";
import { theme } from "@/theme";

export default function AccountTab() {
  const { user, signOut } = useAuth();

  return (
    <Screen title="Account" subtitle="Your plan and device session.">
      <Card>
        <Text style={{ fontWeight: "700", color: theme.colors.ink }}>{user?.displayName}</Text>
        <Text style={{ color: theme.colors.inkMuted, fontSize: 13 }}>{user?.email}</Text>
        <Text style={{ color: theme.colors.inkMuted, fontSize: 13 }}>Exam date: {formatDate(user?.targetDate ?? null)}</Text>
        <Text style={{ color: theme.colors.inkMuted, fontSize: 13 }}>Daily target: {user?.dailyMinutes ?? 30} minutes</Text>
        <Text style={{ color: theme.colors.inkMuted, fontSize: 13 }}>Roles: {user?.roles.join(", ")}</Text>
      </Card>

      {user?.roles.some((role) => ["teacher", "org_admin", "platform_admin"].includes(role)) ? (
        <Button label="Teaching tools" variant="secondary" onPress={() => router.push("/(tabs)")} />
      ) : null}

      <Button
        label="Sign out"
        variant="ghost"
        onPress={() => {
          void (async () => {
            await signOut();
            router.replace("/signin");
          })();
        }}
      />
    </Screen>
  );
}
