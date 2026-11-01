import { API_URL, tokenStore } from "./api";

/**
 * Live classes, exams and consistency for the phone.
 *
 * The room itself stays in the browser — a forwarded WebRTC session plus a canvas whiteboard is the web app's
 * job, and a second implementation would disagree with it about the board's key. What the phone does well is
 * everything around it: finding a sitting, having the link ready, and answering a synchronised exam, which is
 * small enough to sit comfortably in React Native.
 */

export interface PhoneLiveClass {
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

export interface PhoneExam {
  id: string;
  slug: string;
  title: string;
  hostName: string;
  durationMinutes: number;
  startsAt: string;
  endsAt: string;
  shuffled: boolean;
  questionCount: number;
  candidates: number;
  url: string;
  role: "host" | "candidate";
  open: boolean;
  myAttempt: { sessionId: string; status: string; remainingSeconds: number; scorePercent: number | null } | null;
}

export interface PhoneConsistency {
  currentStreak: number;
  longestStreak: number;
  activeDays: number;
  windowDays: number;
  todayActive: boolean;
  streakAtRisk: boolean;
  timeZone: string;
  days: { date: string; active: boolean; isToday: boolean }[];
}

async function get<T>(path: string): Promise<T> {
  const token = await tokenStore.get();
  const response = await fetch(`${API_URL}${path}`, {
    headers: { Accept: "application/json", ...(token ? { Authorization: `Bearer ${token}` } : {}) },
  });
  const payload = (await response.json()) as T & { error?: { message?: string } };
  if (!response.ok) {
    throw new Error(payload.error?.message ?? "The request failed.");
  }
  return payload;
}

export async function fetchLiveClasses(): Promise<PhoneLiveClass[]> {
  const { classes } = await get<{ classes: PhoneLiveClass[] }>("/v1/virtual-classes?scope=all");
  return classes;
}

export async function fetchExams(): Promise<PhoneExam[]> {
  const { exams } = await get<{ exams: PhoneExam[] }>("/v1/exams");
  return exams;
}

export async function fetchConsistency(): Promise<PhoneConsistency> {
  const { consistency } = await get<{ consistency: PhoneConsistency }>("/v1/consistency?days=30");
  return consistency;
}
