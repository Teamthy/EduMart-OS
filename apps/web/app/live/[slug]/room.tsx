"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Copy,
  Eraser,
  Mic,
  MicOff,
  PenLine,
  PhoneOff,
  Radio,
  Send,
  Square,
  Trash2,
  Users,
  Video,
  VideoOff,
} from "lucide-react";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  createBoardKey,
  exportBoardKey,
  importBoardKey,
  openBoardEvent,
  sealBoardEvent,
  type BoardKey,
  type SealedBoardEvent,
} from "@/lib/live-board";

/** The server's view of a class, as this component needs it. */
export interface RoomClass {
  id: string;
  slug: string;
  title: string;
  hostUserId: string;
  hostName: string;
  creditsCost: number;
  status: string;
  startsAt: string;
  durationMinutes: number;
  joinUrl: string;
  role: string;
}

interface Attendee {
  userId: string;
  displayName: string;
  avatar: { initials: string; color: string };
  role: string;
  present: boolean;
}

interface AttendanceRow {
  userId: string;
  displayName: string;
  sessions: number;
  minutes: number;
  creditsPaid: number;
}

interface BoardEventRow extends SealedBoardEvent {
  seq: number;
  authorId: string;
}

interface SignalRow {
  seq: number;
  fromUserId: string;
  toUserId: string | null;
  kind: string;
  payload: string;
}

type Stroke = {
  tool: "pen" | "eraser";
  color: string;
  width: number;
  points: { x: number; y: number }[];
};

/** Every API call in the room goes through here so an error envelope is never surfaced as "undefined". */
async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/v1${path}`, {
    ...init,
    headers: { Accept: "application/json", ...(init?.body ? { "Content-Type": "application/json" } : {}), ...init?.headers },
    cache: "no-store",
  });
  const text = await response.text();
  const payload = text ? JSON.parse(text) : {};
  if (!response.ok) {
    const message = payload?.error?.message ?? "Something went wrong.";
    throw Object.assign(new Error(message), { status: response.status, code: payload?.error?.code });
  }
  return payload as T;
}

const POLL_WAIT = 5;
const HEARTBEAT_MS = 20_000;
const COLORS = ["#1d4ed8", "#b91c1c", "#047857", "#7c3aed", "#b45309"];

export function LiveRoom({ room }: { room: RoomClass }) {
  const isHost = room.role === "host";
  const [me, setMe] = useState<{ id: string; displayName: string } | null>(null);
  const [attendees, setAttendees] = useState<Attendee[]>([]);
  const [status, setStatus] = useState(room.status);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const [joined, setJoined] = useState(false);
  const [discountCode, setDiscountCode] = useState("");
  const [joining, setJoining] = useState(false);

  const [micOn, setMicOn] = useState(true);
  const [cameraOn, setCameraOn] = useState(true);
  const [localStream, setLocalStream] = useState<MediaStream | null>(null);
  const [remotes, setRemotes] = useState<{ userId: string; stream: MediaStream }[]>([]);

  const [tool, setTool] = useState<"pen" | "eraser">("pen");
  const [color, setColor] = useState(COLORS[0]!);
  const [keyReady, setKeyReady] = useState(false);

  const [attendance, setAttendance] = useState<AttendanceRow[] | null>(null);
  const [codes, setCodes] = useState<{ code: string; percentOff: number; uses: number; maxUses: number }[]>([]);

  // Refs hold the machinery that must not re-render: sockets, polls, canvases and keys.
  const boardKeyRef = useRef<BoardKey | null>(null);
  const pendingBoardRef = useRef<BoardEventRow[]>([]);
  const peersRef = useRef(new Map<string, RTCPeerConnection>());
  const channelsRef = useRef(new Map<string, RTCDataChannel>());
  const localsRef = useRef<MediaStream | null>(null);
  const cursorRef = useRef(0);
  const boardSeqRef = useRef(0);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const strokesRef = useRef<Stroke[]>([]);
  const currentStrokeRef = useRef<Stroke | null>(null);
  const iceRef = useRef<RTCIceServer[]>([]);
  const stoppingRef = useRef(false);
  const meRef = useRef<string>("");

  const initials = (name: string) =>
    name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((part) => part[0]?.toUpperCase() ?? "")
      .join("") || "ED";

  /** Redraws the whole board from the stroke list. Cheap at classroom sizes and impossible to get out of order. */
  const redraw = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const context = canvas.getContext("2d");
    if (!context) return;
    context.clearRect(0, 0, canvas.width, canvas.height);
    context.fillStyle = "#ffffff";
    context.fillRect(0, 0, canvas.width, canvas.height);
    for (const stroke of strokesRef.current) drawStroke(context, stroke);
  }, []);

  function drawStroke(context: CanvasRenderingContext2D, stroke: Stroke) {
    if (stroke.points.length === 0) return;
    context.beginPath();
    context.lineJoin = "round";
    context.lineCap = "round";
    context.strokeStyle = stroke.tool === "eraser" ? "#ffffff" : stroke.color;
    context.lineWidth = stroke.width * (stroke.tool === "eraser" ? 4 : 1);
    const [first, ...rest] = stroke.points;
    if (!first) return;
    context.moveTo(first.x, first.y);
    for (const point of rest) context.lineTo(point.x, point.y);
    context.stroke();
  }

  /**
   * Applies a decrypted board event. Applied in the order the server assigned, so two clients that received the
   * same strokes in a different network order still end up with the same picture.
   */
  const applyBoardEvent = useCallback(
    (stroke: Stroke) => {
      strokesRef.current.push(stroke);
      const canvas = canvasRef.current;
      const context = canvas?.getContext("2d");
      if (context) drawStroke(context, stroke);
    },
    [],
  );

  // ── joining ────────────────────────────────────────────────────────────────────────────────────

  const join = useCallback(
    async (code?: string) => {
      setJoining(true);
      setError(null);
      try {
        await call(`/virtual-classes/${room.id}/join`, {
          method: "POST",
          body: JSON.stringify(code ? { discountCode: code } : {}),
        });
        setJoined(true);
      } catch (caught) {
        setError(caught instanceof Error ? caught.message : "Could not join.");
      } finally {
        setJoining(false);
      }
    },
    [room.id],
  );

  // ── identity, ICE and the roster ──────────────────────────────────────────────────────────────

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const [who, ice] = await Promise.all([
          call<{ user: { id: string; displayName: string } }>("/me"),
          call<{ iceServers: RTCIceServer[] }>(`/virtual-classes/${room.id}/ice`),
        ]);
        if (!alive) return;
        setMe(who.user);
        meRef.current = who.user.id;
        iceRef.current = ice.iceServers;
      } catch (caught) {
        if (alive) setError(caught instanceof Error ? caught.message : "Could not load the room.");
      }
    })();
    return () => {
      alive = false;
    };
  }, [room.id]);

  const refreshRoster = useCallback(async () => {
    try {
      const { attendees: roster } = await call<{ attendees: Attendee[] }>(`/virtual-classes/${room.id}/roster`);
      setAttendees(roster);
    } catch {
      // A failed roster poll is not worth an error banner: the next tick will have it.
    }
  }, [room.id]);

  useEffect(() => {
    if (!joined) return;
    void refreshRoster();
    const timer = window.setInterval(() => void refreshRoster(), 10_000);
    return () => window.clearInterval(timer);
  }, [joined, refreshRoster]);

  // ── the media mesh ────────────────────────────────────────────────────────────────────────────

  const sendOverChannel = useCallback((userId: string, message: unknown) => {
    const channel = channelsRef.current.get(userId);
    if (channel && channel.readyState === "open") channel.send(JSON.stringify(message));
  }, []);

  const broadcastKey = useCallback(async () => {
    const key = boardKeyRef.current;
    if (!key) return;
    const encoded = await exportBoardKey(key);
    for (const userId of channelsRef.current.keys()) sendOverChannel(userId, { type: "key", key: encoded });
  }, [sendOverChannel]);

  /** Unseals everything buffered while the room key was still in flight. */
  const drainPending = useCallback(
    async (key: BoardKey) => {
      const pending = pendingBoardRef.current.sort((a, b) => a.seq - b.seq);
      pendingBoardRef.current = [];
      for (const event of pending) {
        try {
          applyBoardEvent(await openBoardEvent<Stroke>(key, event));
        } catch {
          // Not our key. Skip rather than spam: a participant of another class's board cannot be decrypted,
          // and pretending otherwise would silently draw the wrong lesson.
        }
      }
    },
    [applyBoardEvent],
  );

  const createPeer = useCallback(
    (peerId: string) => {
      const existing = peersRef.current.get(peerId);
      if (existing) return existing;
      const peer = new RTCPeerConnection({ iceServers: iceRef.current });

      peer.onicecandidate = (event) => {
        if (!event.candidate) return;
        void call(`/virtual-classes/${room.id}/signal`, {
          method: "POST",
          body: JSON.stringify({ kind: "ice", toUserId: peerId, payload: event.candidate.toJSON().candidate }),
        }).catch(() => undefined);
      };
      peer.ontrack = (event) => {
        const [stream] = event.streams;
        if (!stream) return;
        setRemotes((current) =>
          current.some((item) => item.userId === peerId && item.stream.id === stream.id)
            ? current
            : [...current.filter((item) => item.userId !== peerId), { userId: peerId, stream }],
        );
      };
      peer.onconnectionstatechange = () => {
        if (peer.connectionState === "failed" || peer.connectionState === "closed") {
          setRemotes((current) => current.filter((item) => item.userId !== peerId));
        }
      };

      // The whiteboard key and live strokes travel on this channel. It is also the reason the server never
      // needs to relay board traffic: media and board both go peer to peer.
      const channel = peer.createDataChannel("board");
      channel.onopen = () => {
        channelsRef.current.set(peerId, channel);
        // Whoever holds the key hands it to the newcomer, so a late joiner can replay the stored board.
        void broadcastKey();
        void flushBoard(peerId);
      };
      channel.onclose = () => channelsRef.current.delete(peerId);
      channel.onmessage = async (event) => {
        const message = JSON.parse(String(event.data)) as { type: string; key?: string; stroke?: Stroke; payload?: SealedBoardEvent };
        if (message.type === "key" && message.key) {
          if (boardKeyRef.current) return;
          const key = await importBoardKey(message.key);
          boardKeyRef.current = key;
          setKeyReady(true);
          await drainPending(key);
          return;
        }
        if (message.type === "stroke" && message.payload) {
          const key = boardKeyRef.current;
          if (!key) {
            pendingBoardRef.current.push({ ...message.payload, seq: pendingBoardRef.current.length + 1, authorId: peerId });
            return;
          }
          try {
            applyBoardEvent(await openBoardEvent<Stroke>(key, message.payload));
          } catch {
            // Ignore undecryptable traffic.
          }
        }
      };

      peersRef.current.set(peerId, peer);
      return peer;
    },
    // flushBoard is defined below and stable through useRef-free closure; see note there.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [room.id, broadcastKey, applyBoardEvent, drainPending],
  );

  const flushBoard = useCallback(
    (peerId: string) => {
      // Re-sends the strokes we already drew to a peer that just connected, so a joiner who arrives mid-lesson
      // does not wait for the next stroke to see anything.
      void (async () => {
        const key = boardKeyRef.current;
        if (!key) return;
        for (const stroke of strokesRef.current.slice(-200)) {
          const sealed = await sealBoardEvent(key, stroke);
          sendOverChannel(peerId, { type: "stroke", payload: sealed });
        }
      })();
    },
    [sendOverChannel],
  );

  const attachLocals = useCallback(
    (peer: RTCPeerConnection, stream: MediaStream) => {
      for (const track of stream.getTracks()) peer.addTrack(track, stream);
    },
    [],
  );

  const fetchSignals = useCallback(async () => {
    const { signals } = await call<{ signals: SignalRow[] }>(
      `/virtual-classes/${room.id}/signal?since=${cursorRef.current}&wait=${POLL_WAIT}`,
    );
    for (const signal of signals) {
      cursorRef.current = Math.max(cursorRef.current, signal.seq);
      if (signal.fromUserId === meRef.current) continue;
      const stream = localsRef.current;
      try {
        if (signal.kind === "offer") {
          const peer = createPeer(signal.fromUserId);
          if (stream) attachLocals(peer, stream);
          await peer.setRemoteDescription({ type: "offer", sdp: signal.payload });
          const answer = await peer.createAnswer();
          await peer.setLocalDescription(answer);
          await call(`/virtual-classes/${room.id}/signal`, {
            method: "POST",
            body: JSON.stringify({ kind: "answer", toUserId: signal.fromUserId, payload: answer.sdp }),
          });
        } else if (signal.kind === "answer") {
          const peer = peersRef.current.get(signal.fromUserId);
          if (peer && peer.signalingState === "have-local-offer") {
            await peer.setRemoteDescription({ type: "answer", sdp: signal.payload });
          }
        } else if (signal.kind === "ice") {
          const peer = peersRef.current.get(signal.fromUserId);
          if (peer) await peer.addIceCandidate({ candidate: signal.payload, sdpMid: "0" });
        } else if (signal.kind === "leave") {
          peersRef.current.get(signal.fromUserId)?.close();
          peersRef.current.delete(signal.fromUserId);
          setRemotes((current) => current.filter((item) => item.userId !== signal.fromUserId));
        }
      } catch {
        // A failed negotiation retries on the next roster change; a broken one must not stop the poll loop.
      }
    }
  }, [room.id, createPeer, attachLocals]);

  const startMedia = useCallback(async () => {
    if (localsRef.current) return localsRef.current;
    const stream = await navigator.mediaDevices.getUserMedia({
      // 720p: the resolution that survives a Nigerian mobile uplink and still looks like a person.
      video: { width: { ideal: 1280 }, height: { ideal: 720 }, facingMode: "user" },
      audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    });
    localsRef.current = stream;
    setLocalStream(stream);
    // Every peer already open gets the new tracks without waiting for a renegotiation round trip.
    for (const peer of peersRef.current.values()) {
      attachLocals(peer, stream);
      const offer = await peer.createOffer();
      await peer.setLocalDescription(offer);
      const target = [...peersRef.current.entries()].find(([, value]) => value === peer)?.[0];
      if (target) {
        await call(`/virtual-classes/${room.id}/signal`, {
          method: "POST",
          body: JSON.stringify({ kind: "offer", toUserId: target, payload: offer.sdp }),
        }).catch(() => undefined);
      }
    }
    return stream;
  }, [room.id, attachLocals]);

  /** The peer with the lower id offers, so two participants cannot glare at each other with simultaneous offers. */
  const offerTo = useCallback(
    async (peerId: string) => {
      if (!localsRef.current) return;
      if (meRef.current && meRef.current > peerId) return;
      const peer = createPeer(peerId);
      attachLocals(peer, localsRef.current);
      const offer = await peer.createOffer();
      await peer.setLocalDescription(offer);
      await call(`/virtual-classes/${room.id}/signal`, {
        method: "POST",
        body: JSON.stringify({ kind: "offer", toUserId: peerId, payload: offer.sdp }),
      });
    },
    [room.id, createPeer, attachLocals],
  );

  // Poll the relay for as long as the room is open. The poll doubles as the presence heartbeat.
  useEffect(() => {
    if (!joined) return;
    let running = true;
    (async () => {
      while (running) {
        try {
          await fetchSignals();
        } catch {
          await new Promise((resolve) => setTimeout(resolve, 2000));
        }
      }
    })();
    // A heartbeat with no media yet still keeps this participant out of the "gone quiet" list.
    const heartbeat = window.setInterval(() => {
      void call(`/virtual-classes/${room.id}/signal`, {
        method: "POST",
        body: JSON.stringify({ kind: "presence", payload: "e30" }),
      }).catch(() => undefined);
    }, HEARTBEAT_MS);
    return () => {
      running = false;
      window.clearInterval(heartbeat);
    };
  }, [joined, fetchSignals, room.id]);

  // Opening a connection to everybody else in the room, once, when they appear.
  useEffect(() => {
    if (!joined || !me || !localStream) return;
    for (const attendee of attendees) {
      if (attendee.userId === me.id || attendee.role === "host" && !attendee.present) continue;
      if (!peersRef.current.has(attendee.userId)) void offerTo(attendee.userId).catch(() => undefined);
    }
  }, [attendees, joined, me, localStream, offerTo]);

  // ── the board ─────────────────────────────────────────────────────────────────────────────────

  const sealAndPublish = useCallback(
    async (stroke: Stroke) => {
      const key = boardKeyRef.current;
      if (!key) return;
      const sealed = await sealBoardEvent(key, stroke);
      sendAll(sealed);
      await call(`/virtual-classes/${room.id}/board`, {
        method: "POST",
        body: JSON.stringify(sealed),
      }).catch(() => undefined);
    },
    [room.id],
  );

  const sendAll = useCallback(
    (sealed: SealedBoardEvent) => {
      for (const userId of channelsRef.current.keys()) sendOverChannel(userId, { type: "stroke", payload: sealed });
    },
    [sendOverChannel],
  );

  // The host owns the key; everybody else waits for it over the data channel.
  useEffect(() => {
    if (!joined || !isHost || boardKeyRef.current) return;
    void (async () => {
      boardKeyRef.current = await createBoardKey();
      setKeyReady(true);
      await broadcastKey();
    })();
  }, [joined, isHost, broadcastKey]);

  // Replay what is already on the board, before drawing anything new.
  useEffect(() => {
    if (!joined) return;
    let alive = true;
    (async () => {
      try {
        const { events } = await call<{ events: BoardEventRow[] }>(`/virtual-classes/${room.id}/board?since=0`);
        if (!alive) return;
        boardSeqRef.current = events.reduce((max, event) => Math.max(max, event.seq), 0);
        const key = boardKeyRef.current;
        if (!key) {
          pendingBoardRef.current.push(...events);
          return;
        }
        for (const event of events) {
          try {
            applyBoardEvent(await openBoardEvent<Stroke>(key, event));
          } catch {
            // Another room's key, or a stroke sealed before a rotation. Skip it.
          }
        }
      } catch {
        // An empty board is not an error.
      }
    })();
    return () => {
      alive = false;
    };
  }, [joined, room.id, applyBoardEvent]);

  useEffect(() => {
    // Keeps the drawing surface in step with its CSS box, so a stroke lands where the finger is.
    const canvas = canvasRef.current;
    if (!canvas) return;
    const resize = () => {
      const rect = canvas.getBoundingClientRect();
      const ratio = window.devicePixelRatio || 1;
      canvas.width = Math.round(rect.width * ratio);
      canvas.height = Math.round(rect.height * ratio);
      const context = canvas.getContext("2d");
      if (context) context.scale(ratio, ratio);
      redraw();
    };
    resize();
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, [redraw, joined]);

  function pointerPosition(event: React.PointerEvent<HTMLCanvasElement>) {
    const rect = event.currentTarget.getBoundingClientRect();
    return { x: event.clientX - rect.left, y: event.clientY - rect.top };
  }

  function startStroke(event: React.PointerEvent<HTMLCanvasElement>) {
    if (!joined) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    currentStrokeRef.current = {
      tool,
      color,
      width: 3,
      points: [pointerPosition(event)],
    };
  }

  function extendStroke(event: React.PointerEvent<HTMLCanvasElement>) {
    const stroke = currentStrokeRef.current;
    if (!stroke) return;
    stroke.points.push(pointerPosition(event));
    const context = canvasRef.current?.getContext("2d");
    if (context) {
      const points = stroke.points.slice(-2);
      drawStroke(context, { ...stroke, points });
    }
  }

  function endStroke() {
    const stroke = currentStrokeRef.current;
    currentStrokeRef.current = null;
    if (!stroke || stroke.points.length === 0) return;
    strokesRef.current.push(stroke);
    void sealAndPublish(stroke);
  }

  function clearBoard() {
    // Clearing is itself a stroke-shaped event, so a late joiner replays the clear instead of a board that
    // no longer matches the room.
    const wipe: Stroke = { tool: "eraser", color: "#ffffff", width: 4000, points: [{ x: 0, y: 0 }] };
    strokesRef.current = [];
    redraw();
    void sealAndPublish(wipe);
  }

  // ── leaving, hanging up ───────────────────────────────────────────────────────────────────────

  const hangUp = useCallback(
    async (leavingPage: boolean) => {
      if (stoppingRef.current) return;
      stoppingRef.current = true;
      for (const [userId, peer] of peersRef.current) {
        peer.close();
        if (!leavingPage) {
          await call(`/virtual-classes/${room.id}/signal`, {
            method: "POST",
            body: JSON.stringify({ kind: "leave", toUserId: userId, payload: "e30" }),
          }).catch(() => undefined);
        }
      }
      peersRef.current.clear();
      channelsRef.current.clear();
      localsRef.current?.getTracks().forEach((track) => track.stop());
      localsRef.current = null;
      if (!leavingPage) {
        await call(`/virtual-classes/${room.id}/leave`, { method: "POST" }).catch(() => undefined);
      }
    },
    [room.id],
  );

  useEffect(() => {
    return () => {
      void hangUp(true);
    };
  }, [hangUp]);

  // ── host controls ────────────────────────────────────────────────────────────────────────────

  async function startSession() {
    try {
      await call(`/virtual-classes/${room.id}/start`, { method: "POST" });
      setStatus("live");
      setNotice("Your class is live. The room is open to anyone with the link.");
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not start.");
    }
  }

  async function endSession() {
    try {
      await call(`/virtual-classes/${room.id}/end`, { method: "POST" });
      setStatus("ended");
      await hangUp(false);
      setJoined(false);
      setNotice("Class ended. The attendance report is below.");
      void loadAttendance();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not end the class.");
    }
  }

  async function loadAttendance() {
    try {
      const { attendance: rows } = await call<{ attendance: AttendanceRow[] }>(`/virtual-classes/${room.id}/attendance`);
      setAttendance(rows);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not load attendance.");
    }
  }

  async function loadCodes() {
    try {
      const { discountCodes } = await call<{ discountCodes: typeof codes }>(`/virtual-classes/${room.id}/discount-codes`);
      setCodes(discountCodes);
    } catch {
      // Host-only; a learner never sees this panel.
    }
  }

  async function createCode(percent: number, maxUses: number) {
    try {
      await call(`/virtual-classes/${room.id}/discount-codes`, {
        method: "POST",
        body: JSON.stringify({ percentOff: percent, maxUses }),
      });
      await loadCodes();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not create the code.");
    }
  }

  useEffect(() => {
    if (!isHost) return;
    void loadCodes();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isHost]);

  const remaining = useMemo(() => {
    const endsAt = new Date(room.startsAt).getTime() + room.durationMinutes * 60_000;
    const minutes = Math.max(0, Math.round((endsAt - Date.now()) / 60_000));
    return minutes;
  }, [room.startsAt, room.durationMinutes]);

  // ── render ───────────────────────────────────────────────────────────────────────────────────

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="truncate text-xl">{room.title}</h1>
              <Badge variant={status === "live" ? "success" : status === "ended" ? "outline" : "default"}>
                {status}
              </Badge>
              {room.creditsCost > 0 ? <Badge variant="outline">{room.creditsCost} credits</Badge> : <Badge variant="outline">free</Badge>}
            </div>
            <p className="text-xs text-muted-foreground">
              Hosted by {room.hostName} · {remaining > 0 ? `${remaining} min left of the booking` : "booking window over"}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                void navigator.clipboard?.writeText(room.joinUrl);
                setNotice("Join link copied.");
              }}
            >
              <Copy aria-hidden="true" className="size-4" />
              Copy link
            </Button>
            {isHost && status !== "live" && status !== "ended" ? (
              <Button type="button" size="sm" onClick={() => void startSession()}>
                <Radio aria-hidden="true" className="size-4" />
                Go live
              </Button>
            ) : null}
            {isHost && status !== "ended" ? (
              <Button type="button" size="sm" variant="outline" onClick={() => void endSession()}>
                <Square aria-hidden="true" className="size-4" />
                End class
              </Button>
            ) : null}
          </div>
        </CardContent>
      </Card>

      {error ? <Alert tone="error" title="Something went wrong">{error}</Alert> : null}
      {notice ? <Alert tone="success">{notice}</Alert> : null}

      {!joined ? (
        <Card>
          <CardHeader>
            <CardTitle>Take a seat</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-sm text-muted-foreground">
              {room.creditsCost > 0
                ? `This class costs ${room.creditsCost} credits. Have a discount code? Enter it here.`
                : "This class is free."}
            </p>
            {room.creditsCost > 0 ? (
              <div className="flex flex-wrap items-end gap-3">
                <div className="min-w-48 flex-1">
                  <Label htmlFor="discount">Discount code (optional)</Label>
                  <Input
                    id="discount"
                    value={discountCode}
                    onChange={(event) => setDiscountCode(event.target.value.toUpperCase())}
                    className="font-mono uppercase tracking-widest"
                    placeholder="HALF-OFF"
                  />
                </div>
              </div>
            ) : null}
            <Button type="button" disabled={joining || status === "ended"} onClick={() => void join(discountCode || undefined)}>
              {joining ? "Joining…" : status === "ended" ? "This class has ended" : "Join class"}
            </Button>
          </CardContent>
        </Card>
      ) : (
        <>
          <div className="grid gap-4 lg:grid-cols-[1fr_18rem]">
            <div className="space-y-3">
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="relative overflow-hidden rounded-xl border border-border bg-black">
                  {localStream ? (
                    <VideoTile stream={localStream} muted label={`${me?.displayName ?? "You"} (you)`} />
                  ) : (
                    <div className="flex aspect-video items-center justify-center p-4 text-center text-sm text-white/70">
                      Your camera is off. Turn it on to be seen.
                    </div>
                  )}
                </div>
                {remotes.map((remote) => (
                  <div key={remote.userId} className="relative overflow-hidden rounded-xl border border-border bg-black">
                    <VideoTile
                      stream={remote.stream}
                      label={attendees.find((item) => item.userId === remote.userId)?.displayName ?? "Participant"}
                    />
                  </div>
                ))}
              </div>

              <div className="flex flex-wrap items-center gap-2">
                {!localStream ? (
                  <Button type="button" size="sm" onClick={() => void startMedia().catch(() => setError("Camera and microphone permission is needed."))}>
                    <Video aria-hidden="true" className="size-4" />
                    Turn on camera
                  </Button>
                ) : (
                  <>
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      onClick={() => {
                        const stream = localsRef.current;
                        if (!stream) return;
                        stream.getAudioTracks().forEach((track) => (track.enabled = !micOn));
                        setMicOn(!micOn);
                      }}
                    >
                      {micOn ? <Mic aria-hidden="true" className="size-4" /> : <MicOff aria-hidden="true" className="size-4" />}
                      {micOn ? "Mute" : "Unmute"}
                    </Button>
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      onClick={() => {
                        const stream = localsRef.current;
                        if (!stream) return;
                        stream.getVideoTracks().forEach((track) => (track.enabled = !cameraOn));
                        setCameraOn(!cameraOn);
                      }}
                    >
                      {cameraOn ? <Video aria-hidden="true" className="size-4" /> : <VideoOff aria-hidden="true" className="size-4" />}
                      {cameraOn ? "Camera off" : "Camera on"}
                    </Button>
                  </>
                )}
                <Button type="button" size="sm" variant="outline" onClick={() => void hangUp(false)}>
                  <PhoneOff aria-hidden="true" className="size-4" />
                  Leave
                </Button>
              </div>

              <Card>
                <CardHeader className="flex-row items-center justify-between space-y-0">
                  <CardTitle className="text-base">Whiteboard</CardTitle>
                  <div className="flex flex-wrap items-center gap-1">
                    <Button type="button" size="sm" variant={tool === "pen" ? "default" : "outline"} onClick={() => setTool("pen")}>
                      <PenLine aria-hidden="true" className="size-4" />
                      Pen
                    </Button>
                    <Button type="button" size="sm" variant={tool === "eraser" ? "default" : "outline"} onClick={() => setTool("eraser")}>
                      <Eraser aria-hidden="true" className="size-4" />
                      Eraser
                    </Button>
                    {COLORS.map((swatch) => (
                      <button
                        key={swatch}
                        type="button"
                        aria-label={`Use ${swatch}`}
                        aria-pressed={color === swatch}
                        onClick={() => {
                          setColor(swatch);
                          setTool("pen");
                        }}
                        className="size-6 rounded-full border border-border"
                        style={{ backgroundColor: swatch, outline: color === swatch ? "2px solid var(--foreground)" : "none" }}
                      />
                    ))}
                    <Button type="button" size="sm" variant="outline" onClick={clearBoard}>
                      <Trash2 aria-hidden="true" className="size-4" />
                      Clear
                    </Button>
                  </div>
                </CardHeader>
                <CardContent>
                  <canvas
                    ref={canvasRef}
                    onPointerDown={startStroke}
                    onPointerMove={extendStroke}
                    onPointerUp={endStroke}
                    onPointerLeave={endStroke}
                    className="aspect-video w-full cursor-crosshair touch-none rounded-lg border border-border bg-white"
                    aria-label="Shared whiteboard. Strokes are encrypted before they leave this browser."
                  />
                  <p className="mt-2 text-xs text-muted-foreground">
                    {keyReady
                      ? "Encrypted in this browser. The server stores ciphertext and cannot read the board."
                      : "Waiting for the room key from the host…"}
                  </p>
                </CardContent>
              </Card>
            </div>

            <aside className="space-y-4">
              <Card>
                <CardHeader>
                  <CardTitle className="flex items-center gap-2 text-base">
                    <Users aria-hidden="true" className="size-4" />
                    In the room ({attendees.filter((item) => item.present).length})
                  </CardTitle>
                </CardHeader>
                <CardContent>
                  <ul className="space-y-2">
                    {attendees.map((attendee) => (
                      <li key={attendee.userId} className="flex items-center gap-2">
                        <span
                          className="flex size-8 shrink-0 items-center justify-center rounded-full text-xs font-semibold text-white"
                          style={{ backgroundColor: attendee.avatar.color }}
                          aria-hidden="true"
                        >
                          {attendee.avatar.initials}
                        </span>
                        <span className="min-w-0 flex-1 truncate text-sm">{attendee.displayName}</span>
                        {attendee.role === "host" ? <Badge variant="outline">host</Badge> : null}
                        <span
                          className={`size-2 rounded-full ${attendee.present ? "bg-emerald-500" : "bg-muted-foreground/40"}`}
                          title={attendee.present ? "In the room" : "No heartbeat in the last 75 seconds"}
                        />
                      </li>
                    ))}
                  </ul>
                </CardContent>
              </Card>

              {isHost ? (
                <Card>
                  <CardHeader>
                    <CardTitle className="text-base">Discount codes</CardTitle>
                  </CardHeader>
                  <CardContent className="space-y-3">
                    <DiscountForm onCreate={createCode} />
                    <ul className="space-y-1 text-xs">
                      {codes.map((code) => (
                        <li key={code.code} className="flex items-center justify-between gap-2 font-mono">
                          <span>{code.code}</span>
                          <span className="text-muted-foreground">
                            {code.percentOff}% · {code.uses}/{code.maxUses === 0 ? "∞" : code.maxUses}
                          </span>
                        </li>
                      ))}
                      {codes.length === 0 ? <li className="text-muted-foreground">No codes yet.</li> : null}
                    </ul>
                  </CardContent>
                </Card>
              ) : null}

              {isHost ? (
                <Card>
                  <CardHeader>
                    <CardTitle className="text-base">Attendance</CardTitle>
                  </CardHeader>
                  <CardContent className="space-y-2">
                    <Button type="button" size="sm" variant="outline" onClick={() => void loadAttendance()}>
                      <Send aria-hidden="true" className="size-4" />
                      Refresh report
                    </Button>
                    {attendance ? (
                      <ul className="space-y-1 text-xs">
                        {attendance.map((row) => (
                          <li key={row.userId} className="flex items-center justify-between gap-2">
                            <span className="truncate">{row.displayName}</span>
                            <span className="text-muted-foreground">
                              {row.minutes} min · {row.creditsPaid} credits
                            </span>
                          </li>
                        ))}
                        {attendance.length === 0 ? <li className="text-muted-foreground">Nobody yet.</li> : null}
                      </ul>
                    ) : (
                      <p className="text-xs text-muted-foreground">
                        Minutes are measured from heartbeats, not asserted by the client.
                      </p>
                    )}
                  </CardContent>
                </Card>
              ) : null}
            </aside>
          </div>
        </>
      )}
    </div>
  );
}

function VideoTile({ stream, muted, label }: { stream: MediaStream; muted?: boolean; label: string }) {
  const ref = useRef<HTMLVideoElement | null>(null);
  useEffect(() => {
    if (ref.current) ref.current.srcObject = stream;
  }, [stream]);
  return (
    <>
      <video ref={ref} autoPlay playsInline muted={muted} className="aspect-video w-full object-cover" />
      <span className="absolute inset-x-0 bottom-0 truncate bg-black/50 px-2 py-1 text-xs text-white">{label}</span>
    </>
  );
}

function DiscountForm({ onCreate }: { onCreate: (percent: number, maxUses: number) => Promise<void> }) {
  const [percent, setPercent] = useState(50);
  const [maxUses, setMaxUses] = useState(10);
  return (
    <div className="flex flex-wrap items-end gap-2">
      <div className="w-20">
        <Label htmlFor="percent">%</Label>
        <Input
          id="percent"
          type="number"
          min={1}
          max={100}
          value={percent}
          onChange={(event) => setPercent(Number(event.target.value))}
        />
      </div>
      <div className="w-24">
        <Label htmlFor="maxUses">Uses</Label>
        <Input
          id="maxUses"
          type="number"
          min={0}
          value={maxUses}
          onChange={(event) => setMaxUses(Number(event.target.value))}
        />
      </div>
      <Button type="button" size="sm" onClick={() => void onCreate(percent, maxUses)}>
        Create
      </Button>
    </div>
  );
}
