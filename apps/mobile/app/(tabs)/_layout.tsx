import { Tabs } from "expo-router";
import { Text } from "react-native";

import { theme } from "@/theme";

/** Tab icons are text glyphs to keep the bundle small; a vector icon set can drop in later. */
function TabGlyph({ label, focused }: { label: string; focused: boolean }) {
  return (
    <Text style={{ fontSize: 18, color: focused ? theme.colors.primary : theme.colors.inkMuted }} aria-hidden>
      {label}
    </Text>
  );
}

export default function TabsLayout() {
  return (
    <Tabs
      screenOptions={{
        headerStyle: { backgroundColor: theme.colors.background },
        headerShadowVisible: false,
        tabBarActiveTintColor: theme.colors.primary,
        tabBarInactiveTintColor: theme.colors.inkMuted,
        tabBarStyle: { minHeight: 56 },
      }}
    >
      <Tabs.Screen
        name="index"
        options={{ title: "Home", tabBarIcon: ({ focused }) => <TabGlyph label="⌂" focused={focused} /> }}
      />
      <Tabs.Screen
        name="practice"
        options={{ title: "Practice", tabBarIcon: ({ focused }) => <TabGlyph label="✎" focused={focused} /> }}
      />
      <Tabs.Screen
        name="live"
        options={{ title: "Live", tabBarIcon: ({ focused }) => <TabGlyph label="◉" focused={focused} /> }}
      />
      <Tabs.Screen
        name="progress"
        options={{ title: "Progress", tabBarIcon: ({ focused }) => <TabGlyph label="▤" focused={focused} /> }}
      />
      <Tabs.Screen
        name="account"
        options={{ title: "Account", tabBarIcon: ({ focused }) => <TabGlyph label="☺" focused={focused} /> }}
      />
    </Tabs>
  );
}
