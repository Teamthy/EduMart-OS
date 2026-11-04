/**
 * End-to-end encryption for the room's audio and video.
 *
 * # What changes, and what it costs
 *
 * A fifty-seat room cannot be a mesh, so media goes through the forwarder in `apps/api/internal/media`, which means
 * the server decrypts every hop (DTLS-SRTP) and could, in principle, read the packets it copies. Whiteboard strokes
 * and chat were already end-to-end; audio and video were not, and `docs/VIRTUAL_CLASSES.md` said so.
 *
 * This module closes that gap *in the encoded pipeline*: frames are encrypted in the sender's browser before the
 * packet is built and decrypted in each receiver's browser after it arrives, so the bytes the media server forwards
 * are ciphertext. It does not remove the server from the path — it removes the server's ability to read the payload.
 *
 * # Why a worker, and why two code paths
 *
 * The pipeline hook is worker-only. `RTCRtpScriptTransform` is the current one (Chrome 111+, Safari 15.4+ under a
 * flag, Firefox 117+); `createEncodedStreams()` is the older one that Chromium shipped first. Both are supported here
 * because the population that matters is not one browser: a learner on a three-year-old Android phone and a tutor on
 * a new laptop have to be able to hear each other, and a room where half the people are silent is worse than one
 * that says what it is doing.
 *
 * # What it cannot do
 *
 * A browser with neither hook — or a page served over plain HTTP, where WebCrypto is unavailable — falls back to
 * hop-by-hop encryption and the room **says so**, because a green padlock that means nothing is a lie with a
 * friendly face. `mode` is reported to the UI for exactly that reason.
 */

/** How the room's media is protected, as the UI reports it. */
export type MediaEncryptionMode =
  /** Frames are encrypted in the sender's browser and decrypted in each receiver's. */
  | "e2ee"
  /** DTLS-SRTP per hop only: the forwarder terminates both legs and could read the payload. */
  | "hop-by-hop";

/** Which pipeline hook this browser offers. */
export type MediaPipeline = "script-transform" | "encoded-streams" | null;

export interface MediaE2EESupport {
  supported: boolean;
  pipeline: MediaPipeline;
  /** Why it is unavailable, in words a learner can act on. Empty when supported. */
  reason: string;
}

/** The worker script, served as written from `public/` because a worker is not bundled. */
export const MEDIA_WORKER_URL = "/media-e2ee-worker.js";

/**
 * Reports whether this browser can encrypt in the pipeline.
 *
 * Three conditions, each of which is a real way this fails: a secure context (WebCrypto does not exist on plain
 * HTTP), a pipeline hook, and `Worker`. The caller must treat `supported: false` as "hop-by-hop", never as an error
 * to throw — a room that cannot encrypt should still work.
 */
export function mediaE2EESupport(): MediaE2EESupport {
  if (typeof window === "undefined") {
    return { supported: false, pipeline: null, reason: "This is not a browser." };
  }
  if (typeof window.isSecureContext === "boolean" && !window.isSecureContext) {
    return {
      supported: false,
      pipeline: null,
      reason: "Media encryption needs HTTPS. Over plain HTTP the browser has no WebCrypto.",
    };
  }
  if (!globalThis.crypto?.subtle) {
    return { supported: false, pipeline: null, reason: "This browser has no WebCrypto, so media cannot be encrypted." };
  }
  if (typeof Worker === "undefined") {
    return { supported: false, pipeline: null, reason: "This browser cannot run the encryption worker." };
  }
  if (typeof (globalThis as { RTCRtpScriptTransform?: unknown }).RTCRtpScriptTransform === "function") {
    return { supported: true, pipeline: "script-transform", reason: "" };
  }
  if (typeof window.RTCRtpSender !== "undefined" && "createEncodedStreams" in window.RTCRtpSender.prototype) {
    return { supported: true, pipeline: "encoded-streams", reason: "" };
  }
  return {
    supported: false,
    pipeline: null,
    reason:
      "This browser has no hook for encrypting encoded frames, so the room falls back to hop-by-hop encryption.",
  };
}

/** Creates the worker. The caller owns it and must terminate it when the connection ends. */
export function createMediaWorker(): Worker {
  return new Worker(MEDIA_WORKER_URL);
}

/**
 * Hands the room key to the worker.
 *
 * The bytes are **transferred**, not copied: after this call the main thread no longer holds a second reference, and
 * the key exists in the worker scope the page cannot reach. The room key is the one the whiteboard already uses —
 * the worker derives its own media key from it, so the two nonce spaces stay apart.
 */
export function handRoomKey(worker: Worker, raw: Uint8Array): void {
  // A *copy*, transferred. The room key still lives in the page (the whiteboard needs it), so handing over the
  // original buffer would detach it underneath the board. The copy is what makes the transfer safe — and a transfer
  // rather than a structured clone is what keeps a second reachable copy from existing after this call.
  const copy = new Uint8Array(new ArrayBuffer(raw.byteLength));
  copy.set(raw);
  worker.postMessage({ type: "key", raw: copy.buffer }, [copy.buffer]);
}

/**
 * A sealer for anything that is not a live RTP frame — the recorder's file chunks, today.
 *
 * It derives the *same* HKDF key the worker does, from the same room key, with the same salt and info. Reusing the
 * derivation is deliberate: there is one media key per room, and a second one with a hand-typed label would be a second
 * thing to get wrong. The nonce space stays safe because every seal generates a fresh 96-bit IV, and a recording
 * produces a handful of chunks per second rather than the hundreds a stream does.
 *
 * The key is imported non-extractable, so even code on this page cannot read it back out of the CryptoKey.
 */
export async function mediaFrameCodec(roomKey: Uint8Array): Promise<{
  seal: (plaintext: Uint8Array) => Promise<Uint8Array>;
  open: (sealed: Uint8Array) => Promise<Uint8Array | null>;
}> {
  const subtle = globalThis.crypto.subtle;
  const base = await subtle.importKey("raw", roomKey as unknown as BufferSource, "HKDF", false, ["deriveKey"]);
  const key = await subtle.deriveKey(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt: new TextEncoder().encode("edumart/media/v1"),
      info: new TextEncoder().encode("rtp-frame"),
    },
    base,
    { name: "AES-GCM", length: 256 },
    false,
    ["encrypt", "decrypt"],
  );

  return {
    async seal(plaintext) {
      const iv = new Uint8Array(new ArrayBuffer(12));
      globalThis.crypto.getRandomValues(iv);
      const body = new Uint8Array(
        await subtle.encrypt({ name: "AES-GCM", iv, tagLength: 128 }, key, plaintext as unknown as BufferSource),
      );
      // Same frame layout as the worker: [version][iv][ciphertext||tag]. One format, one parser, one thing to remember.
      const out = new Uint8Array(new ArrayBuffer(1 + iv.byteLength + body.byteLength));
      out[0] = 0x01;
      out.set(iv, 1);
      out.set(body, 1 + iv.byteLength);
      return out;
    },
    async open(sealed) {
      if (sealed.byteLength <= 13 || sealed[0] !== 0x01) return null;
      try {
        // Copies rather than views: a `Uint8Array<ArrayBufferLike>` subarray is not a `BufferSource` to this TypeScript
        // version, and a 13-byte copy per chunk costs nothing against a decrypt.
        const iv = new Uint8Array(sealed.subarray(1, 13));
        const body = new Uint8Array(sealed.subarray(13));
        const plaintext = await subtle.decrypt({ name: "AES-GCM", iv, tagLength: 128 }, key, body);
        return new Uint8Array(plaintext);
      } catch {
        // A chunk from another room, a truncated download, or a tampered byte. All of them are "not ours".
        return null;
      }
    },
  };
}

/** Narrow structural types: the DOM lib in this TypeScript version does not declare every insertable-stream hook. */
interface EncodedStreams {
  readable: ReadableStream;
  writable: WritableStream;
}
interface TransformableSender {
  createEncodedStreams?: () => EncodedStreams;
  transform?: unknown;
}
interface TransformableReceiver {
  createEncodedStreams?: () => EncodedStreams;
  transform?: unknown;
}

type ScriptTransformConstructor = new (worker: Worker, options?: { operation: "encode" | "decode" }) => unknown;

function scriptTransform(worker: Worker, operation: "encode" | "decode"): unknown | null {
  const ctor = (globalThis as { RTCRtpScriptTransform?: ScriptTransformConstructor }).RTCRtpScriptTransform;
  if (typeof ctor !== "function") return null;
  return new ctor(worker, { operation });
}

/**
 * Attaches encryption to a sender, or reports that it could not.
 *
 * Returns false rather than throwing: the caller keeps the connection either way, and a room that failed to encrypt
 * one direction is still a room. `mode` in the UI is what tells the truth about it.
 */
export function attachSenderEncryption(sender: unknown, worker: Worker): boolean {
  const target = sender as TransformableSender;
  const transform = scriptTransform(worker, "encode");
  if (transform !== null) {
    target.transform = transform;
    return true;
  }
  if (typeof target.createEncodedStreams === "function") {
    // The older path: hand the actual pipeline streams to the worker. They are transferred, which is what removes
    // the main thread from the media path entirely.
    const streams = target.createEncodedStreams();
    worker.postMessage(
      { type: "streams", readable: streams.readable, writable: streams.writable, options: { operation: "encode" } },
      [streams.readable, streams.writable],
    );
    return true;
  }
  return false;
}

/** Attaches decryption to a receiver. Returns false when this browser offers no hook. */
export function attachReceiverEncryption(receiver: unknown, worker: Worker): boolean {
  const target = receiver as TransformableReceiver;
  const transform = scriptTransform(worker, "decode");
  if (transform !== null) {
    target.transform = transform;
    return true;
  }
  if (typeof target.createEncodedStreams === "function") {
    const streams = target.createEncodedStreams();
    worker.postMessage(
      { type: "streams", readable: streams.readable, writable: streams.writable, options: { operation: "decode" } },
      [streams.readable, streams.writable],
    );
    return true;
  }
  return false;
}

/**
 * A frame that could not be authenticated costs one frame, not the call.
 *
 * The worker reports failures in batches; this turns that into the sentence the room shows. Exported so the wording
 * lives in one place and a test can assert it without a browser.
 */
export function describeDecryptFailures(count: number): string {
  return `${count} media frame${count === 1 ? "" : "s"} could not be decrypted. That usually means somebody in the room does not hold this class key.`;
}
