import { ActivityIndicator, Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import type { ReactNode } from "react";

import { theme } from "../theme";

export function Screen({ children, title, subtitle }: { children: ReactNode; title?: string; subtitle?: string }) {
  return (
    <View style={styles.screen}>
      {title ? (
        <Text style={styles.title} accessibilityRole="header">
          {title}
        </Text>
      ) : null}
      {subtitle ? <Text style={styles.subtitle}>{subtitle}</Text> : null}
      {children}
    </View>
  );
}

export function Card({ children }: { children: ReactNode }) {
  return <View style={styles.card}>{children}</View>;
}

export function Button({
  label,
  onPress,
  variant = "primary",
  loading = false,
  disabled = false,
}: {
  label: string;
  onPress: () => void;
  variant?: "primary" | "secondary" | "ghost";
  loading?: boolean;
  disabled?: boolean;
}) {
  const isDisabled = disabled || loading;
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityState={{ disabled: isDisabled, busy: loading }}
      onPress={onPress}
      disabled={isDisabled}
      style={({ pressed }) => [
        styles.button,
        variant === "primary" && styles.buttonPrimary,
        variant === "secondary" && styles.buttonSecondary,
        variant === "ghost" && styles.buttonGhost,
        (pressed || isDisabled) && styles.buttonPressed,
      ]}
    >
      {loading ? <ActivityIndicator color={variant === "primary" ? "#ffffff" : theme.colors.primary} /> : null}
      <Text style={[styles.buttonLabel, variant === "primary" ? styles.buttonLabelPrimary : styles.buttonLabelSecondary]}>
        {label}
      </Text>
    </Pressable>
  );
}

export function Field({
  label,
  value,
  onChangeText,
  placeholder,
  error,
  secureTextEntry,
  keyboardType,
  autoCapitalize,
  hint,
}: {
  label: string;
  value: string;
  onChangeText: (value: string) => void;
  placeholder?: string;
  error?: string;
  secureTextEntry?: boolean;
  keyboardType?: "default" | "email-address" | "numeric";
  autoCapitalize?: "none" | "words" | "characters";
  hint?: string;
}) {
  return (
    <View style={styles.field}>
      <Text style={styles.label}>{label}</Text>
      <TextInput
        value={value}
        onChangeText={onChangeText}
        placeholder={placeholder}
        placeholderTextColor={theme.colors.inkMuted}
        secureTextEntry={secureTextEntry}
        keyboardType={keyboardType}
        autoCapitalize={autoCapitalize}
        accessibilityLabel={label}
        accessibilityHint={hint}
        style={[styles.input, error ? styles.inputError : null]}
      />
      {error ? <Text style={styles.error}>{error}</Text> : hint ? <Text style={styles.hint}>{hint}</Text> : null}
    </View>
  );
}

export function Banner({ tone, children }: { tone: "info" | "error" | "success"; children: ReactNode }) {
  return (
    <View
      accessibilityRole={tone === "error" ? "alert" : "text"}
      style={[styles.banner, tone === "error" && styles.bannerError, tone === "success" && styles.bannerSuccess]}
    >
      <Text style={styles.bannerText}>{children}</Text>
    </View>
  );
}

export function Choice({
  label,
  detail,
  selected,
  onPress,
  accessibilityRole = "radio",
}: {
  label: string;
  detail?: string;
  selected: boolean;
  onPress: () => void;
  accessibilityRole?: "radio" | "checkbox";
}) {
  return (
    <Pressable
      accessibilityRole={accessibilityRole}
      accessibilityState={{ checked: selected }}
      onPress={onPress}
      style={[styles.choice, selected && styles.choiceSelected]}
    >
      <View style={[styles.marker, selected && styles.markerSelected]} />
      <View style={styles.choiceBody}>
        <Text style={styles.choiceLabel}>{label}</Text>
        {detail ? <Text style={styles.choiceDetail}>{detail}</Text> : null}
      </View>
    </Pressable>
  );
}

export function StepHeader({ step, total, label }: { step: number; total: number; label: string }) {
  return (
    <View style={styles.stepHeader} accessibilityLabel={`Step ${step} of ${total}, ${label}`}>
      <View style={styles.stepTrack}>
        {Array.from({ length: total }).map((_, index) => (
          <View key={index} style={[styles.stepSegment, index < step && styles.stepSegmentDone]} />
        ))}
      </View>
      <Text style={styles.stepLabel}>
        Step {step} of {total} · {label}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  screen: {
    flex: 1,
    backgroundColor: theme.colors.background,
    padding: theme.spacing.lg,
    gap: theme.spacing.md,
  },
  title: { fontSize: 24, fontWeight: "700", color: theme.colors.ink },
  subtitle: { fontSize: 14, color: theme.colors.inkMuted, marginTop: -8 },
  card: {
    backgroundColor: theme.colors.surface,
    borderRadius: theme.radii.md,
    borderWidth: 1,
    borderColor: theme.colors.border,
    padding: theme.spacing.md,
    gap: theme.spacing.sm,
  },
  button: {
    minHeight: theme.touch,
    borderRadius: theme.radii.sm,
    paddingHorizontal: theme.spacing.lg,
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 8,
  },
  buttonPrimary: { backgroundColor: theme.colors.primary },
  buttonSecondary: { backgroundColor: theme.colors.primarySoft },
  buttonGhost: { backgroundColor: "transparent" },
  buttonPressed: { opacity: 0.75 },
  buttonLabel: { fontSize: 15, fontWeight: "700" },
  buttonLabelPrimary: { color: "#ffffff" },
  buttonLabelSecondary: { color: theme.colors.primaryDark },
  field: { gap: 6 },
  label: { fontSize: 14, fontWeight: "600", color: theme.colors.ink },
  input: {
    minHeight: theme.touch,
    borderWidth: 1,
    borderColor: theme.colors.border,
    borderRadius: theme.radii.sm,
    paddingHorizontal: theme.spacing.md,
    backgroundColor: theme.colors.surface,
    fontSize: 16,
    color: theme.colors.ink,
  },
  inputError: { borderColor: theme.colors.danger },
  error: { fontSize: 12, color: theme.colors.danger, fontWeight: "600" },
  hint: { fontSize: 12, color: theme.colors.inkMuted },
  banner: { backgroundColor: theme.colors.primarySoft, borderRadius: theme.radii.sm, padding: theme.spacing.md },
  bannerError: { backgroundColor: theme.colors.dangerSoft },
  bannerSuccess: { backgroundColor: theme.colors.successSoft },
  bannerText: { fontSize: 13, color: theme.colors.ink },
  choice: {
    flexDirection: "row",
    alignItems: "flex-start",
    gap: theme.spacing.md,
    borderWidth: 1,
    borderColor: theme.colors.border,
    borderRadius: theme.radii.md,
    padding: theme.spacing.md,
    backgroundColor: theme.colors.surface,
    minHeight: theme.touch,
  },
  choiceSelected: { borderColor: theme.colors.primary, backgroundColor: theme.colors.primarySoft },
  marker: {
    width: 20,
    height: 20,
    borderRadius: 10,
    borderWidth: 2,
    borderColor: theme.colors.border,
    marginTop: 2,
  },
  markerSelected: { borderColor: theme.colors.primary, backgroundColor: theme.colors.primary },
  choiceBody: { flex: 1, gap: 2 },
  choiceLabel: { fontSize: 15, fontWeight: "600", color: theme.colors.ink },
  choiceDetail: { fontSize: 12, color: theme.colors.inkMuted },
  stepHeader: { gap: 6 },
  stepTrack: { flexDirection: "row", gap: 4 },
  stepSegment: { flex: 1, height: 6, borderRadius: 3, backgroundColor: theme.colors.border },
  stepSegmentDone: { backgroundColor: theme.colors.primary },
  stepLabel: { fontSize: 12, fontWeight: "700", color: theme.colors.primary },
});
