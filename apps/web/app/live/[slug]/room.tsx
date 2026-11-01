"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Copy, Mic, MicOff, PhoneOff, PenLine, Eraser, Radio, Send, Square, Trash2, Users, Video, VideoOff } from "lucide-react";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  LiveApiError,
  appendBoard,
  connectMedia,
  createDiscountCode,
  endLiveClass,
  joinLiveClass,
  liveAttendance,
  liveBoard,
  liveDiscountCodes,
  liveIce,
  liveRoster,
  pollSignals,
  postSignal,
  leaveLiveClass,
  setStage,
  startLiveClass,
  type AttendanceRow,
  type BoardEventRow,
  type LiveClass,
  type StageSeat,
} from "@/lib/live-api";
import { createBoardKey, exportBoardKey, importBoardKey, openBoardEvent, sealBoardEvent, type BoardKey } from "@/lib/live-board";

/** Six seats on the stage: the host plus five speakers. The media forwarder is built around this number. */
const STAGE_SLOTS = 6;

type Stroke = {
  tool: "pen" | "eraser";
  color: string;
  width: number;
  points: { x: number; y: number }[];
};

type ChatMessage = { from: string; name: string; text: string; at: number };

interface Props {
  initial: LiveClass;
  viewerId: string;
  viewerName: string;
  /** The board key from the link fragment, when this browser was given one. */
  linkKey: string | null;
}

/**
 * The room.
 *
 * Two transport decisions live here, and both are consequences of the 50-seat requirement:
 *
 * 1. **Media is forwarded, not meshed.** Every participant holds one peer connection to the media forwarder and
 *    the server copies packets to the people who want them. The connection's shape is fixed — six stage slots —
 *    so a promotion changes which publisher feeds a slot rather than renegotiating anybody mid-sentence.
 * 2. **The whiteboard key travels in the link's fragment, not through the server.** A fragment is never sent to
 *    a server, so the room key reaches participants without the API ever seeing it, and the board events the API
 *    stores stay opaque. A visitor who opened `/live/SLUG` without the fragment can watch, hear and chat, but
 *    the board is blank for them and the page says why.
 *
 * Chat and presence ride the signalling relay, which the API stores for ten minutes and then drops.
 */
export function LiveRoom({ initial, viewerId, viewerName, linkKey }: Props) {
  const [room, setRoom] = useState<LiveClass>(initial);
  const [seats, setSeats] = useState<StageSeat[]>([]);
  const [joined, setJoined] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [discountCode, setDiscountCode] = useState("");
  const [joining, setJoining] = useState(false);

  const [localStream, setLocalStream] = useState<MediaStream | null>(null);
  const [slotStreams, setSlotStreams] = useState<Record<number, MediaStream>>({});
  const [mediaState, setMediaState] = useState<"idle" | "connecting" | "live" | "unavailable">("idle");
  const [micOn, setMicOn] = useState(true);
  const [cameraOn, setCameraOn] = useState(true);

  const [tool, setTool] = useState<"pen" | "eraser">("pen");
  const [color, setColor] = useState("#1d4ed8");
  const [keyReady, setKeyReady] = useState(false);
  const [boardNotice, setBoardNotice] = useState<string | null>(null);

  const [chat, setChat] = useState<ChatMessage[]>([]);
  const [draft, setDraft] = useState("");
  const [attendance, setAttendance] = useState<AttendanceRow[] | null>(null);
  const [codes, setCodes] = useState<{ code: string; percentOff: number; uses: number; maxUses: number }[]>([]);

  const isHost = room.role === "host";
  const mySeat = seats.find((seat) => seat.userId === viewerId);
  const publishing = mySeat ? mySeat.stageRole !== "audience" : false;
  const mySlot = mySeat?.stageSlot ?? -1;

  const peerRef = useRef<RTCPeerConnection | null>(null);
  const localsRef = useRef<MediaStream | null>(null);
  const closingRef = useRef(false);
  const signalCursorRef = useRef(0);
  const boardSeqRef = useRef(0);
  const boardKeyRef = useRef<BoardKey | null>(null);
  const pendingStrokesRef = useRef<BoardEventRow[]>([]);
  const strokesRef = useRef<Stroke[]>([]);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const currentStrokeRef = useRef<Stroke | null>(null);
  const iceRef = useRef<RTCIceServer[]>([]);
  const joinedRef = useRef(false);
  const publishingRef = useRef(false);
  const chatNamesRef = useRef(new Map<string, string>());

  useEffect(() => {
    joinedRef.current = joined;
  }, [joined]);
  useEffect(() => {
    publishingRef.current = publishing;
  }, [publishing]);

  // ── the board key ─────────────────────────────────────────────────────────────────────────────

  useEffect(() => {
    let alive = true;
    // The fragment is read here rather than passed down, because a fragment is the one part of a URL a browser
    // never sends to a server: the page cannot see it, only the client can.
    const fromFragment = (() => {
      if (linkKey) return linkKey;
      if (typeof window === "undefined") return null;
      const hash = window.location.hash.replace(/^#/, "");
      const params = new URLSearchParams(hash);
      return params.get("k");
    })();
    (async () => {
      if (fromFragment) {
        try {
          const key = await importBoardKey(fromFragment);
          if (!alive) return;
          boardKeyRef.current = key;
          setKeyReady(true);
          await drainPending(key);
        } catch {
          if (alive) setBoardNotice("The board key in this link is not usable. Ask the host for the board link.");
        }
        return;
      }
      if (!isHost) {
        setBoardNotice("This link has no board key, so the board stays encrypted and blank here. Ask the host for the board link.");
        return;
      }
      // The host owns the key. It goes into the URL fragment — which no browser sends to a server — so "copy
      // link" hands it out without the API ever seeing it.
      const key = await createBoardKey();
      if (!alive) return;
      boardKeyRef.current = key;
      setKeyReady(true);
      const url = new URL(window.location.href);
      url.hash = `k=${exportBoardKey(key)}`;
      window.history.replaceState(null, "", url.toString());
      setBoardNotice("You hold this room's board key. Use “Copy link” so the people you invite can read the board.");
    })();
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isHost, linkKey]);

  const boardLink = useCallback(() => {
    const key = boardKeyRef.current;
    const base = room.joinUrl;
    if (!key) return base;
    return `${base}#k=${exportBoardKey(key)}`;
  }, [room.joinUrl]);

  // ── drawing ────────────────────────────────────────────────────────────────────────────────────

  const redraw = useCallback(() => {
    const canvas = canvasRef.current;
    const context = canvas?.getContext("2d");
    if (!canvas || !context) return;
    context.clearRect(0, 0, canvas.width, canvas.height);
    for (const stroke of strokesRef.current) drawStroke(context, stroke);
  }, []);

  const applyStroke = useCallback(
    (stroke: Stroke) => {
      if (stroke.tool === "eraser" && stroke.width > 1000) {
        strokesRef.current = [];
        redraw();
        return;
      }
      strokesRef.current.push(stroke);
      const context = canvasRef.current?.getContext("2d");
      if (context) drawStroke(context, stroke);
    },
    [redraw],
  );

  async function drainPending(key: BoardKey) {
    const pending = pendingStrokesRef.current.sort((a, b) => a.seq - b.seq);
    pendingStrokesRef.current = [];
    for (const event of pending) {
      try {
        applyStroke(await openBoardEvent<Stroke>(key, event));
      } catch {
        // Sealed under another room's key: skip rather than draw the wrong lesson.
      }
    }
  }

  function drawStroke(context: CanvasRenderingContext2D, stroke: Stroke) {
    if (stroke.points.length === 0) return;
    context.strokeStyle = stroke.color;
    context.lineWidth = stroke.width;
    context.lineCap = "round";
    context.lineJoin = "round";
    if (stroke.tool === "eraser") context.strokeStyle = "#ffffff";
    context.beginPath();
    const [first, ...rest] = stroke.points;
    if (!first) return;
    context.moveTo(first.x, first.y);
    for (const point of rest) context.lineTo(point.x, point.y);
    context.stroke();
  }

  // ── joining, roster and presence ──────────────────────────────────────────────────────────────

  const join = useCallback(
    async (code?: string) => {
      setJoining(true);
      setError(null);
      try {
        const { class: joinedClass } = await joinLiveClass(room.id, code);
        setRoom(joinedClass);
        setJoined(true);
      } catch (caught) {
        setError(caught instanceof Error ? caught.message : "Could not join.");
      } finally {
        setJoining(false);
      }
    },
    [room.id],
  );

  // A host who created the class is already seated; everybody else takes a seat explicitly so the free window,
  // the capacity and the price are all decided by the server before any media is negotiated.
  useEffect(() => {
    if (initial.role === "host") {
      void join();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const refreshRoster = useCallback(async () => {
    try {
      const { attendees } = await liveRoster(room.id);
      setSeats(attendees);
      for (const seat of attendees) chatNamesRef.current.set(seat.userId, seat.displayName);
    } catch {
      // A failed poll is not worth a banner; the next tick has it.
    }
  }, [room.id]);

  useEffect(() => {
    if (!joined) return;
    void refreshRoster();
    const timer = window.setInterval(() => void refreshRoster(), 10_000);
    const heartbeat = window.setInterval(() => {
      void postSignal(room.id, { kind: "presence", payload: "e30" }).catch(() => undefined);
    }, 20_000);
    return () => {
      window.clearInterval(timer);
      window.clearInterval(heartbeat);
    };
  }, [joined, refreshRoster, room.id]);

  // ── the board relay ──────────────────────────────────────────────────────────────────────────

  const flushRemoteBoard = useCallback(
    async (since: number) => {
      const { events } = await liveBoard(room.id, since);
      for (const event of events) {
        boardSeqRef.current = Math.max(boardSeqRef.current, event.seq);
        if (event.authorId === viewerId) continue;
        const key = boardKeyRef.current;
        if (!key) {
          pendingStrokesRef.current.push(event);
          continue;
        }
        try {
          applyStroke(await openBoardEvent<Stroke>(key, event));
        } catch {
          // Undecryptable: another room's key, or a stroke sealed before the key rotated.
        }
      }
    },
    [applyStroke, room.id, viewerId],
  );

  useEffect(() => {
    if (!joined) return;
    let alive = true;
    (async () => {
      await flushRemoteBoard(0).catch(() => undefined);
    })();
    const timer = window.setInterval(() => {
      if (!alive) return;
      void flushRemoteBoard(boardSeqRef.current).catch(() => undefined);
    }, 2_000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [joined, flushRemoteBoard]);

  // ── signalling: chat and presence ────────────────────────────────────────────────────────────

  useEffect(() => {
    if (!joined) return;
    let running = true;
    (async () => {
      while (running && !closingRef.current) {
        try {
          const { signals } = await pollSignals(room.id, signalCursorRef.current, 20);
          for (const signal of signals) {
            signalCursorRef.current = Math.max(signalCursorRef.current, signal.seq);
            if (signal.fromUserId === viewerId) continue;
            if (signal.kind === "chat") {
              try {
                const payload = JSON.parse(signal.payload) as { text?: string };
                const text = payload.text;
                if (!text) continue;
                setChat((current) => [
                  ...current.slice(-99),
                  {
                    from: signal.fromUserId,
                    name: chatNamesRef.current.get(signal.fromUserId) ?? "Someone",
                    text: text.slice(0, 500),
                    at: Date.now(),
                  },
                ]);
              } catch {
                // A malformed chat payload is dropped, not rendered.
              }
            }
          }
        } catch {
          await new Promise((resolve) => setTimeout(resolve, 2_000));
        }
      }
    })();
    return () => {
      running = false;
    };
  }, [joined, room.id, viewerId]);

  // ── media: one connection to the forwarder ───────────────────────────────────────────────────

  const teardownMedia = useCallback(() => {
    peerRef.current?.close();
    peerRef.current = null;
    setSlotStreams({});
    localsRef.current?.getTracks().forEach((track) => track.stop());
    localsRef.current = null;
    setLocalStream(null);
    setMediaState("idle");
  }, []);

  /**
   * The offer this browser makes.
   *
   * The m-line order is the forwarder's fixed layout: for a publisher, one audio and one video m-line to send on,
   * then six video+audio pairs to receive the stage. An audience member offers only the receive pairs. The
   * server's answer lines up one for one — which is why nothing here ever needs to be renegotiated.
   */
  const connect = useCallback(async () => {
    if (closingRef.current) return;
    teardownMedia();
    setMediaState("connecting");
    try {
      const { iceServers } = await liveIce(room.id);
      iceRef.current = iceServers;
      const peer = new RTCPeerConnection({ iceServers });
      peerRef.current = peer;

      const canPublish = publishingRef.current;
      if (canPublish) {
        try {
          const stream = await navigator.mediaDevices.getUserMedia({
            video: { width: { ideal: 1280 }, height: { ideal: 720 }, facingMode: "user" },
            audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
          });
          localsRef.current = stream;
          setLocalStream(stream);
          for (const track of stream.getTracks()) {
            peer.addTransceiver(track, { direction: "sendonly", streams: [stream] });
          }
        } catch {
          // No camera or microphone: still connect as a publisher so the stage keeps its shape, and say why the
          // tile is black rather than pretending the device works.
          setNotice("Camera and microphone permission was not granted. You are on stage without sending media.");
          peer.addTransceiver("audio", { direction: "sendonly" });
          peer.addTransceiver("video", { direction: "sendonly" });
        }
      }

      for (let slot = 0; slot < STAGE_SLOTS; slot += 1) {
        peer.addTransceiver("video", { direction: "recvonly" });
        peer.addTransceiver("audio", { direction: "recvonly" });
      }

      peer.ontrack = (event) => {
        const mid = event.transceiver.mid;
        if (mid === null) return;
        const index = Number(mid);
        if (Number.isNaN(index)) return;
        const base = canPublish ? 2 : 0;
        const offset = index - base;
        if (offset < 0) return; // one of our own sending m-lines
        const slot = Math.floor(offset / 2);
        const isVideo = offset % 2 === 0;
        if (!isVideo) {
          if (slot === mySlot) return; // never play our own audio back at us
          const audio = new Audio();
          audio.srcObject = event.streams[0] ?? new MediaStream([event.track]);
          audio.autoplay = true;
          void audio.play().catch(() => undefined);
          return;
        }
        const stream = event.streams[0] ?? new MediaStream([event.track]);
        setSlotStreams((current) => ({ ...current, [slot]: stream }));
      };

      peer.onconnectionstatechange = () => {
        if (peer.connectionState === "failed") {
          setNotice("The media connection dropped. Reconnecting…");
          void connect().catch(() => undefined);
        }
      };

      const offer = await peer.createOffer();
      await peer.setLocalDescription(offer);
      // Non-trickle on the server side: the answer already contains the forwarder's candidates.
      const answer = await connectMedia(room.id, offer.sdp ?? "");
      await peer.setRemoteDescription({ type: "answer", sdp: answer.sdp });
      setMediaState("live");
    } catch (caught) {
      teardownMedia();
      setMediaState("unavailable");
      setError(
        caught instanceof LiveApiError
          ? caught.message
          : "The media server could not be reached. The board, roster and chat still work.",
      );
    }
  }, [mySlot, room.id, teardownMedia]);

  // Connect once the room knows the caller's seat: the seat decides whether they publish, and the answer's shape
  // depends on that.
  useEffect(() => {
    if (!joined || !mySeat) return;
    if (room.status === "ended") return;
    void connect();
    return () => teardownMedia();
    // Reconnects when the stage role or slot changes, which is exactly when the answer must be rebuilt.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [joined, mySeat?.stageRole, mySeat?.stageSlot, room.status]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const resize = () => {
      const rect = canvas.getBoundingClientRect();
      const ratio = window.devicePixelRatio || 1;
      canvas.width = Math.round(rect.width * ratio);
      canvas.height = Math.round(rect.height * ratio);
      const context = canvas.getContext("2d");
      if (context) context.setTransform(ratio, 0, 0, ratio, 0, 0);
      redraw();
    };
    resize();
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, [redraw, joined]);

  // ── publishing a stroke ───────────────────────────────────────────────────────────────────────

  const publishStroke = useCallback(
    async (stroke: Stroke) => {
      const key = boardKeyRef.current;
      if (!key) {
        setBoardNotice("You can see the class, but you have no board key, so your strokes would be unreadable.");
        return;
      }
      try {
        const sealed = await sealBoardEvent(key, stroke);
        const { seq } = await appendBoard(room.id, sealed);
        boardSeqRef.current = Math.max(boardSeqRef.current, seq);
      } catch (caught) {
        setError(caught instanceof Error ? caught.message : "Could not save that stroke.");
      }
    },
    [room.id],
  );

  function pointerPosition(event: React.PointerEvent<HTMLCanvasElement>) {
    const rect = event.currentTarget.getBoundingClientRect();
    return { x: event.clientX - rect.left, y: event.clientY - rect.top };
  }

  function startStroke(event: React.PointerEvent<HTMLCanvasElement>) {
    if (!joined) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    currentStrokeRef.current = { tool, color, width: tool === "eraser" ? 16 : 3, points: [pointerPosition(event)] };
  }

  function extendStroke(event: React.PointerEvent<HTMLCanvasElement>) {
    const stroke = currentStrokeRef.current;
    if (!stroke) return;
    stroke.points.push(pointerPosition(event));
    const context = canvasRef.current?.getContext("2d");
    if (context) drawStroke(context, { ...stroke, points: stroke.points.slice(-2) });
  }

  function endStroke() {
    const stroke = currentStrokeRef.current;
    currentStrokeRef.current = null;
    if (!stroke || stroke.points.length === 0) return;
    applyStroke(stroke);
    void publishStroke(stroke);
  }

  function clearBoard() {
    // A clear is itself a stroke-shaped event, so a late joiner replays the clear instead of a board that no
    // longer matches the room.
    const wipe: Stroke = { tool: "eraser", color: "#ffffff", width: 4000, points: [{ x: 0, y: 0 }] };
    strokesRef.current = [];
    redraw();
    void publishStroke(wipe);
  }

  // ── leaving and host controls ────────────────────────────────────────────────────────────────

  const hangUp = useCallback(
    async (leavingPage: boolean) => {
      if (closingRef.current) return;
      closingRef.current = true;
      teardownMedia();
      if (!leavingPage) {
        await leaveLiveClass(room.id).catch(() => undefined);
      }
    },
    [room.id, teardownMedia],
  );

  useEffect(() => {
    return () => {
      void hangUp(true);
    };
  }, [hangUp]);

  async function startSession() {
    try {
      const { class: started } = await startLiveClass(room.id);
      setRoom(started);
      setNotice("Your class is live. Everybody with the link can take a seat.");
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not start the class.");
    }
  }

  async function endSession() {
    try {
      const { class: ended } = await endLiveClass(room.id);
      setRoom(ended);
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
      const { attendance: rows } = await liveAttendance(room.id);
      setAttendance(rows);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not load the attendance report.");
    }
  }

  async function loadCodes() {
    try {
      const { discountCodes } = await liveDiscountCodes(room.id);
      setCodes(discountCodes);
    } catch {
      // Host-only panel.
    }
  }

  async function makeCode(percent: number, maxUses: number) {
    try {
      await createDiscountCode(room.id, percent, maxUses);
      await loadCodes();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not create the code.");
    }
  }

  async function promote(userId: string, role: "speaker" | "audience") {
    try {
      const { attendees } = await setStage(room.id, userId, role);
      setSeats(attendees);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Could not change the stage.");
    }
  }

  useEffect(() => {
    if (!isHost) return;
    void loadCodes();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isHost]);

  function sendChat() {
    const text = draft.trim();
    if (!text) return;
    setDraft("");
    void postSignal(room.id, { kind: "chat", payload: JSON.stringify({ text }) }).catch(() =>
      setError("That message did not send."),
    );
  }

  const remaining = useMemo(() => {
    const endsAt = new Date(room.startsAt).getTime() + room.durationMinutes * 60_000;
    return Math.max(0, Math.round((endsAt - Date.now()) / 60_000));
  }, [room.startsAt, room.durationMinutes]);

  const stageTiles = useMemo(() => {
    const bySlot = new Map<number, StageSeat>();
    for (const seat of seats) {
      if (seat.stageSlot >= 0) bySlot.set(seat.stageSlot, seat);
    }
    return Array.from({ length: STAGE_SLOTS }, (_, slot) => ({ slot, seat: bySlot.get(slot) ?? null }));
  }, [seats]);

  // ── render ───────────────────────────────────────────────────────────────────────────────────

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="truncate text-xl">{room.title}</h1>
              <Badge variant={room.status === "live" ? "success" : room.status === "ended" ? "outline" : "default"}>
                {room.status}
              </Badge>
              {room.creditsCost > 0 ? (
                <Badge variant="outline">{room.creditsCost} credits</Badge>
              ) : (
                <Badge variant="outline">free · 45 min</Badge>
              )}
              <Badge variant="outline">
                {seats.length}/{room.capacity} seats
              </Badge>
            </div>
            <p className="text-xs text-muted-foreground">
              Hosted by {room.hostName} ·{" "}
              {room.creditsCost > 0
                ? `${remaining} min left of the booking`
                : `free sessions close 45 minutes after they start`}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                void navigator.clipboard?.writeText(boardLink());
                setNotice(
                  keyReady
                    ? "Join link copied, with the board key in its fragment."
                    : "Join link copied. It carries no board key, so the board will stay blank for whoever opens it.",
                );
              }}
            >
              <Copy aria-hidden="true" className="size-4" />
              Copy link
            </Button>
            {isHost && room.status !== "live" && room.status !== "ended" ? (
              <Button type="button" size="sm" onClick={() => void startSession()}>
                <Radio aria-hidden="true" className="size-4" />
                Go live
              </Button>
            ) : null}
            {isHost && room.status !== "ended" ? (
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
                ? `This class costs ${room.creditsCost} credits. A discount code goes here.`
                : "This class is free, and closes 45 minutes after it starts."}
            </p>
            <p className="text-sm text-muted-foreground">
              {room.capacity} seats, six of them on stage. You can watch, listen and chat from any seat; the host
              promotes speakers.
            </p>
            {room.creditsCost > 0 ? (
              <Input
                value={discountCode}
                onChange={(event) => setDiscountCode(event.target.value.toUpperCase())}
                className="max-w-56 font-mono uppercase tracking-widest"
                placeholder="DISCOUNT CODE"
                aria-label="Discount code"
              />
            ) : null}
            <Button
              type="button"
              disabled={joining || room.status === "ended"}
              onClick={() => void join(discountCode || undefined)}
            >
              {joining ? "Joining…" : room.status === "ended" ? "This class has ended" : "Join class"}
            </Button>
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-4 lg:grid-cols-[1fr_18rem]">
          <div className="space-y-3">
            <div className="grid gap-2 sm:grid-cols-3">
              {stageTiles.map(({ slot, seat }) => {
                const isMe = seat?.userId === viewerId;
                const stream = isMe ? localStream : slotStreams[slot];
                return (
                  <div key={slot} className="relative overflow-hidden rounded-xl border border-border bg-black">
                    {stream ? (
                      <VideoTile stream={stream} muted={isMe} label={seat?.displayName ?? "Seat"} />
                    ) : (
                      <div className="flex aspect-video items-center justify-center p-2 text-center text-xs text-white/60">
                        {seat ? `${seat.displayName} is on stage` : slot === 0 ? "Host seat" : "Empty seat"}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <Badge variant="outline">
                {mediaState === "live"
                  ? publishing
                    ? "On stage"
                    : "Watching the stage"
                  : mediaState === "connecting"
                    ? "Connecting media…"
                    : mediaState === "unavailable"
                      ? "Media unavailable"
                      : "Not connected"}
              </Badge>
              {localStream ? (
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
              ) : null}
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => {
                  void hangUp(false);
                  setJoined(false);
                }}
              >
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
                  {["#1d4ed8", "#b91c1c", "#047857", "#7c3aed", "#b45309"].map((swatch) => (
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
                  aria-label="Shared whiteboard. Strokes are encrypted in this browser before they leave it."
                />
                <p className="mt-2 text-xs text-muted-foreground">
                  {keyReady
                    ? "Strokes are encrypted in this browser. The server stores ciphertext and cannot read the board."
                    : boardNotice ?? "No board key in this link, so the board stays blank here."}
                </p>
              </CardContent>
            </Card>
          </div>

          <aside className="space-y-4">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <Users aria-hidden="true" className="size-4" />
                  In the room ({seats.filter((seat) => seat.present).length})
                </CardTitle>
              </CardHeader>
              <CardContent>
                <ul className="space-y-2">
                  {seats.map((seat) => (
                    <li key={seat.userId} className="flex items-center gap-2">
                      <span
                        className={`size-2 shrink-0 rounded-full ${seat.present ? "bg-emerald-500" : "bg-muted-foreground/40"}`}
                        title={seat.present ? "In the room" : "No heartbeat in the last 75 seconds"}
                      />
                      <span className="min-w-0 flex-1 truncate text-sm">{seat.displayName}</span>
                      {seat.stageRole !== "audience" ? <Badge variant="outline">{seat.stageRole}</Badge> : null}
                      {isHost && seat.userId !== viewerId ? (
                        <button
                          type="button"
                          className="text-xs text-primary underline-offset-2 hover:underline"
                          onClick={() => void promote(seat.userId, seat.stageRole === "audience" ? "speaker" : "audience")}
                        >
                          {seat.stageRole === "audience" ? "Bring on stage" : "Remove"}
                        </button>
                      ) : null}
                    </li>
                  ))}
                  {seats.length === 0 ? <li className="text-xs text-muted-foreground">Nobody yet.</li> : null}
                </ul>
              </CardContent>
            </Card>

            <Card>
              <CardHeader>
                <CardTitle className="text-base">Chat</CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                <ul className="max-h-56 space-y-1 overflow-y-auto text-xs">
                  {chat.map((message, index) => (
                    <li key={`${message.at}-${index}`}>
                      <span className="font-medium">{message.name === viewerName ? "You" : message.name}: </span>
                      {message.text}
                    </li>
                  ))}
                  {chat.length === 0 ? <li className="text-muted-foreground">No messages yet.</li> : null}
                </ul>
                <form
                  className="flex items-center gap-2"
                  onSubmit={(event) => {
                    event.preventDefault();
                    sendChat();
                  }}
                >
                  <Input
                    value={draft}
                    onChange={(event) => setDraft(event.target.value)}
                    placeholder="Message the room"
                    aria-label="Chat message"
                  />
                  <Button type="submit" size="sm" variant="outline">
                    <Send aria-hidden="true" className="size-4" />
                  </Button>
                </form>
              </CardContent>
            </Card>

            {isHost ? (
              <Card>
                <CardHeader>
                  <CardTitle className="text-base">Discount codes</CardTitle>
                </CardHeader>
                <CardContent className="space-y-3">
                  <DiscountForm onCreate={makeCode} />
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
                      Minutes are measured from heartbeats the server records, not asserted by the client.
                    </p>
                  )}
                </CardContent>
              </Card>
            ) : null}
          </aside>
        </div>
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
      <label className="text-xs">
        <span className="block text-muted-foreground">Percent off</span>
        <Input
          type="number"
          min={1}
          max={100}
          value={percent}
          onChange={(event) => setPercent(Number(event.target.value))}
          className="w-20"
        />
      </label>
      <label className="text-xs">
        <span className="block text-muted-foreground">Uses</span>
        <Input
          type="number"
          min={0}
          value={maxUses}
          onChange={(event) => setMaxUses(Number(event.target.value))}
          className="w-24"
        />
      </label>
      <Button type="button" size="sm" onClick={() => void onCreate(percent, maxUses)}>
        Create
      </Button>
    </div>
  );
}
