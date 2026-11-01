import { useCallback, useEffect, useState } from "react";
import { Linking, ScrollView, Text } from "react-native";

import { formatDateTimeSafe } from "@edumart/shared";

import { API_URL, tokenStore } from "@/api";
import { Banner, Button, Card, Screen } from "@/components/ui";
import { theme } from "@/theme";

interface LiveClass {
  id: string;
  slug: string;
  title: string;
  creditsCost: number;
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
 * The room itself opens in the browser rather than inside the app: a mesh WebRTC call plus a canvas whiteboard
 * is the web app's job, and two implementations of the same room would disagree about the whiteboard's key and
 * about presence. What the phone is genuinely good at is the part that happens before and after — finding the
 * class and having the link ready — so that is what this screen owns.
 */
export default function LiveTab() {
  const [classes, setClasses] = useState<LiveClass[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const token = await tokenStore.get();
      const response = await fetch(`${API_URL}/v1/virtual-classes?scope=all`, {
        headers: { Accept: "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}) },
      });
      const payload = await response.json();
      if (!response.ok) {
        setError(payload?.error?.message ?? "We couldn't load your live classes.");
        return;
      }
      setClasses(payload.classes as LiveClass[]);
    } catch {
      setError("Check your connection and try again.");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <ScrollView>
      <Screen title="Live classes" subtitle="Rooms you host or are invited to. Opening one hands off to the browser, where the video and whiteboard live.">
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
              {item.creditsCost > 0 ? ` · ${item.creditsCost} credits` : " · free"}
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

        <Button label="Refresh" variant="secondary" onPress={() => void load()} />
      </Screen>
    </ScrollView>
  );
}
