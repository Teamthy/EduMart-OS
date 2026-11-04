"use client";

import { mediaFrameCodec } from "./live-media-e2ee";

/**
 * Recording a class, from inside the room.
 *
 * There is no server-side recorder, and that is a consequence rather than a shortcut: audio and video are sealed in the
 * publisher's browser and opened in each subscriber's (`live-media-e2ee.ts`), so the process that forwards packets
 * holds ciphertext and has nothing it could record. A recording therefore has exactly one place it can be made —
 * a participant's browser, with the key everyone in the room already has.
 *
 * So this composes what the recorder can see and hear (a canvas of the stage tiles plus a mixed audio graph), seals the
 * file exactly as the call itself is sealed, and uploads chunks that the server stores and cannot read.
 *
 * Four decisions are load-bearing:
 *
 *  1. **Consent is in the mixer, not in the UI.** {@link StageRecorder.start} takes the set of participant ids that
 *     acknowledged the recording notice, and only draws and mixes those people. Drawing everybody and hiding a banner
 *     would make the notice decorative.
 *  2. **The file is sealed before it is uploaded.** The same HKDF-derived media key, via `mediaFrameCodec`, so the
 *     server's disk only ever sees ciphertext and the recording is exactly as private as the call was.
 *  3. **A chunk that fails to upload is retried, and a recording that loses a chunk says so.** Silence would produce a
 *     file with a hole in it and no way to tell.
 *  4. **Recording stops when the room does.** The recorder is bound to the connection's lifetime; a page that closes
 *     mid-class is a recording that ends where the page did, which is honest and is what the store's `failed` status is
 *     for.
 */

/** The uploader talks to the API. Injected so tests can drive the recorder without a server. */
export interface RecordingUploader {
  /** Upload one sealed chunk. Resolves when it is stored; rejects to trigger a retry. */
  put(seq: number, bytes: Uint8Array, durationMS: number): Promise<void>;
  /** Tell the server capture has stopped. */
  complete(): Promise<void>;
}

export interface StageRecorderOptions {
  /** Where sealed chunks go. Injected so the recorder has no opinion about HTTP. */
  uploader: RecordingUploader;
  /** The room key, the same bytes the call uses. The recorder derives its own sealing key from them. */
  roomKey: Uint8Array;
  /** Participant id → stream. The recorder filters this against {@link acknowledgedUserIds} itself. */
  streams: Array<{ userId: string; stream: MediaStream; label: string }>;
  /** Who was told, and said so. Nobody outside this set appears in the file. */
  acknowledgedUserIds: string[];
  /** Called as chunks are uploaded, for a progress line the host can see. */
  onProgress?: (state: { chunks: number; bytes: number; failed: number }) => void;
  onError?: (message: string) => void;
  /** How long each chunk covers, in milliseconds. Four seconds keeps a lost chunk small and uploads frequent. */
  chunkMs?: number;
}

export interface RecorderSupport {
  supported: boolean;
  /** Why not, in a sentence a host can act on. Empty when supported. */
  reason: string;
}

/**
 * Whether this browser can record a class at all.
 *
 * Checked rather than assumed: `MediaRecorder`, canvas capture and `AudioContext` are three separate capabilities and a
 * browser can have two of them. A host who presses record on a browser that cannot do it should be told before the
 * class, not during it.
 */
export function recorderSupport(): RecorderSupport {
  if (typeof window === "undefined") return { supported: false, reason: "Recording runs in the browser." };
  const missing: string[] = [];
  if (typeof window.MediaRecorder === "undefined") missing.push("MediaRecorder");
  const canvas = document.createElement("canvas");
  if (typeof canvas.captureStream !== "function") missing.push("canvas capture");
  const audio = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
  if (!audio) missing.push("audio mixing");
  if (missing.length === 0) return { supported: true, reason: "" };
  return {
    supported: false,
    reason: `This browser cannot record (no ${missing.join(", ")}). Chrome, Edge and a recent Firefox can.`,
  };
}

/** What a recording produced, so the caller can report it honestly rather than assuming success. */
export interface RecordingOutcome {
  chunks: number;
  bytes: number;
  failedChunks: number;
}

const DEFAULT_CHUNK_MS = 4000;
/** 1280×720 is enough to read a face and a slide, and a quarter of the memory of 1080p on a phone. */
const CANVAS_WIDTH = 1280;
const CANVAS_HEIGHT = 720;
const FRAME_RATE = 24;

/**
 * One recording session.
 *
 * Usage: construct, `start()`, `stop()`. Every method is idempotent enough that a double-click on "stop recording" does
 * not produce two finished files or a rejected promise the UI has to swallow.
 */
export class StageRecorder {
  private readonly options: StageRecorderOptions;
  private readonly canvas: HTMLCanvasElement;
  private readonly context: CanvasRenderingContext2D | null;
  private readonly audioContext: AudioContext | null;
  private readonly audioDestination: MediaStreamAudioDestinationNode | null;
  private mediaRecorder: MediaRecorder | null = null;
  private codec: Awaited<ReturnType<typeof mediaFrameCodec>> | null = null;
  private drawn: Array<{ userId: string; stream: MediaStream; label: string }> = [];
  private videoElements: HTMLVideoElement[] = [];
  private drawTimer: number | null = null;
  private queue: Promise<void> = Promise.resolve();
  private seq = 0;
  private bytes = 0;
  private failed = 0;
  private chunkStartedAt = 0;
  private stopping = false;

  constructor(options: StageRecorderOptions) {
    this.options = options;
    this.canvas = document.createElement("canvas");
    this.canvas.width = CANVAS_WIDTH;
    this.canvas.height = CANVAS_HEIGHT;
    this.context = this.canvas.getContext("2d");
    const AudioContextClass =
      window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    this.audioContext = AudioContextClass ? new AudioContextClass() : null;
    this.audioDestination = this.audioContext?.createMediaStreamDestination() ?? null;
  }

  /**
   * The people who are allowed into the file.
   *
   * Exported as a function because it is the consent control, and a test should be able to check it without starting a
   * recorder: given the streams in the room and the acknowledged ids, this is exactly who gets drawn.
   */
  static includeOnly(
    streams: Array<{ userId: string; stream: MediaStream; label: string }>,
    acknowledgedUserIds: string[],
  ): Array<{ userId: string; stream: MediaStream; label: string }> {
    const allowed = new Set(acknowledgedUserIds);
    return streams.filter((entry) => allowed.has(entry.userId) && entry.stream.getTracks().length > 0);
  }

  async start(): Promise<void> {
    if (this.mediaRecorder) return;
    if (!this.context) throw new Error("This browser cannot draw the stage, so it cannot record it.");

    this.codec = await mediaFrameCodec(this.options.roomKey);
    this.drawn = StageRecorder.includeOnly(this.options.streams, this.options.acknowledgedUserIds);
    if (this.drawn.length === 0) {
      // A recording of nothing is a file that says a class happened. Better to fail here, where the host is looking.
      throw new Error("Nobody in the file yet: the people on stage have not acknowledged the recording notice.");
    }

    // Hidden <video> elements per stream: a canvas draws from a video element, and the element has to be playing for
    // frames to arrive. They are kept out of the layout rather than removed, because a detached element stops decoding.
    this.videoElements = this.drawn.map((entry) => {
      const element = document.createElement("video");
      element.srcObject = entry.stream;
      element.muted = true;
      element.playsInline = true;
      element.style.position = "fixed";
      element.style.opacity = "0";
      element.style.pointerEvents = "none";
      element.style.width = "1px";
      element.style.height = "1px";
      document.body.appendChild(element);
      void element.play().catch(() => undefined);
      return element;
    });

    // Audio: each participant's tracks into one destination. Gain is left at 1; the mixer is not a compressor and
    // should not pretend to be one.
    if (this.audioContext && this.audioDestination) {
      for (const entry of this.drawn) {
        for (const track of entry.stream.getAudioTracks()) {
          try {
            this.audioContext.createMediaStreamSource(new MediaStream([track])).connect(this.audioDestination);
          } catch {
            // A track that cannot be routed (already consumed elsewhere, or ended) is skipped rather than aborting the
            // recording: losing one voice is better than losing the class.
          }
        }
      }
    }

    this.drawFrame();
    this.drawTimer = window.setInterval(() => this.drawFrame(), Math.round(1000 / FRAME_RATE));

    const composed = new MediaStream();
    for (const track of this.canvas.captureStream(FRAME_RATE).getVideoTracks()) composed.addTrack(track);
    if (this.audioDestination) {
      for (const track of this.audioDestination.stream.getAudioTracks()) composed.addTrack(track);
    }

    const mimeType = pickMimeType();
    this.mediaRecorder = new MediaRecorder(composed, {
      ...(mimeType ? { mimeType } : {}),
      // Deliberately modest: a class is a talking head and a whiteboard, and 1.2 Mbit/s keeps an hour near 500 MB
      // before sealing. A higher rate buys detail nobody watching on a phone can use.
      videoBitsPerSecond: 1_200_000,
      audioBitsPerSecond: 64_000,
    });
    this.chunkStartedAt = Date.now();
    this.mediaRecorder.ondataavailable = (event) => {
      if (event.data.size === 0) return;
      const duration = Date.now() - this.chunkStartedAt;
      this.chunkStartedAt = Date.now();
      // Chunks are queued, not uploaded in parallel: the sequence number is the file's order, and two uploads racing
      // would let chunk 5 land before chunk 4 and produce a file that does not play.
      this.queue = this.queue.then(() => this.uploadChunk(event.data, duration));
    };
    this.mediaRecorder.onerror = () => {
      this.options.onError?.("The browser stopped recording. Anything already uploaded is safe.");
    };
    this.mediaRecorder.start(this.options.chunkMs ?? DEFAULT_CHUNK_MS);
  }

  /** Finishes the file: stops capture, waits for the last chunk to upload, and tells the server. */
  async stop(): Promise<RecordingOutcome> {
    if (this.stopping) return { chunks: this.seq, bytes: this.bytes, failedChunks: this.failed };
    this.stopping = true;

    if (this.mediaRecorder && this.mediaRecorder.state !== "inactive") {
      const stopped = new Promise<void>((resolve) => {
        this.mediaRecorder!.onstop = () => resolve();
      });
      this.mediaRecorder.stop();
      await stopped;
    }
    // The final `dataavailable` fires before `stop`, so awaiting the queue here means the last chunk is in before the
    // server is told the file is complete.
    await this.queue;
    this.teardown();

    try {
      await this.options.uploader.complete();
    } catch (error) {
      this.options.onError?.(error instanceof Error ? error.message : "The server did not confirm the recording.");
    }
    return { chunks: this.seq, bytes: this.bytes, failedChunks: this.failed };
  }

  private async uploadChunk(blob: Blob, durationMS: number): Promise<void> {
    if (!this.codec) return;
    const seq = ++this.seq;
    try {
      const raw = new Uint8Array(await blob.arrayBuffer());
      const sealed = await this.codec.seal(raw);
      // Three attempts with backoff: a class happens once, and a single dropped chunk is a hole in the only copy.
      let lastError: unknown = null;
      for (let attempt = 0; attempt < 3; attempt++) {
        try {
          await this.options.uploader.put(seq, sealed, durationMS);
          this.bytes += sealed.byteLength;
          this.options.onProgress?.({ chunks: this.seq, bytes: this.bytes, failed: this.failed });
          return;
        } catch (error) {
          lastError = error;
          await new Promise((resolve) => setTimeout(resolve, 500 * (attempt + 1)));
        }
      }
      throw lastError;
    } catch {
      this.failed += 1;
      this.options.onError?.(
        `Part ${seq} of the recording could not be uploaded. The file will be missing that part.`,
      );
      this.options.onProgress?.({ chunks: this.seq, bytes: this.bytes, failed: this.failed });
    }
  }

  /** Draws the stage: an even grid of the acknowledged participants, with names. */
  private drawFrame(): void {
    const context = this.context;
    if (!context) return;
    context.fillStyle = "#0b1220";
    context.fillRect(0, 0, CANVAS_WIDTH, CANVAS_HEIGHT);

    const count = this.videoElements.length;
    if (count === 0) return;
    const columns = Math.ceil(Math.sqrt(count));
    const rows = Math.ceil(count / columns);
    const cellWidth = Math.floor(CANVAS_WIDTH / columns);
    const cellHeight = Math.floor(CANVAS_HEIGHT / rows);

    this.videoElements.forEach((element, index) => {
      const column = index % columns;
      const row = Math.floor(index / columns);
      const x = column * cellWidth;
      const y = row * cellHeight;
      if (element.readyState >= 2 && element.videoWidth > 0) {
        // Contain, not stretch: a portrait phone video in a landscape cell keeps its aspect ratio and gets bars.
        const scale = Math.min(cellWidth / element.videoWidth, cellHeight / element.videoHeight);
        const width = element.videoWidth * scale;
        const height = element.videoHeight * scale;
        context.drawImage(element, x + (cellWidth - width) / 2, y + (cellHeight - height) / 2, width, height);
      }
      const label = this.drawn[index]?.label ?? "";
      if (label) {
        context.fillStyle = "rgba(0,0,0,0.55)";
        context.fillRect(x + 8, y + cellHeight - 40, Math.min(cellWidth - 16, label.length * 11 + 16), 30);
        context.fillStyle = "#ffffff";
        context.font = "20px system-ui, sans-serif";
        context.fillText(label, x + 16, y + cellHeight - 18);
      }
    });
  }

  private teardown(): void {
    if (this.drawTimer !== null) {
      window.clearInterval(this.drawTimer);
      this.drawTimer = null;
    }
    for (const element of this.videoElements) {
      element.srcObject = null;
      element.remove();
    }
    this.videoElements = [];
    void this.audioContext?.close().catch(() => undefined);
  }
}

/** The first container this browser will agree to. WebM with VP8/Opus is what every capable browser has in common. */
export function pickMimeType(): string | null {
  if (typeof window === "undefined" || typeof window.MediaRecorder === "undefined") return null;
  const candidates = [
    "video/webm;codecs=vp8,opus",
    "video/webm;codecs=vp9,opus",
    "video/webm",
    "video/mp4",
  ];
  for (const candidate of candidates) {
    if (window.MediaRecorder.isTypeSupported(candidate)) return candidate;
  }
  return null;
}

/**
 * Builds an uploader that talks to the class recording endpoints.
 *
 * Kept out of the recorder itself so the recorder has no opinion about HTTP, and a test can watch the chunk stream
 * without a server.
 */
export function createRecordingUploader(input: {
  classId: string;
  fetchImpl?: typeof fetch;
  onUnauthorised?: () => void;
}): RecordingUploader {
  const doFetch = input.fetchImpl ?? ((...args: Parameters<typeof fetch>) => fetch(...args));
  const base = `/v1/virtual-classes/${encodeURIComponent(input.classId)}/recording`;

  const put = async (seq: number, bytes: Uint8Array, durationMS: number) => {
    const response = await doFetch(`${base}/segments?seq=${seq}&durationMs=${Math.max(0, Math.round(durationMS))}`, {
      method: "POST",
      // The bytes are already sealed; sending them as octet-stream avoids the third that base64 would add.
      headers: { "Content-Type": "application/octet-stream" },
      body: bytes as unknown as BodyInit,
      credentials: "include",
    });
    if (response.status === 401) input.onUnauthorised?.();
    if (!response.ok) {
      const text = await response.text().catch(() => "");
      throw new Error(`chunk ${seq} rejected (${response.status}) ${text.slice(0, 120)}`);
    }
  };

  const complete = async () => {
    const response = await doFetch(`${base}/complete`, { method: "POST", credentials: "include" });
    if (!response.ok) throw new Error(`the server did not confirm the recording (${response.status})`);
  };

  return { put, complete };
}
