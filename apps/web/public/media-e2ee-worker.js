/**
 * Edumart media end-to-end encryption — the worker that runs inside the WebRTC pipeline.
 *
 * Why a worker: encoded frames are only reachable through the pipeline hooks a browser exposes, and both of them
 * (`RTCRtpScriptTransform` and `createEncodedStreams`) run on the worker side of a dedicated worker. Encrypting on
 * the main thread would mean holding the room key in a context every script on the page shares; in a worker the
 * key exists in one scope that no page script can reach.
 *
 * What it does to each frame:
 *
 *     [0x01][12-byte random IV][AES-GCM ciphertext || 16-byte tag]
 *
 * A frame is the unit a codec already produces, so encrypting whole frames keeps the RTP header — SSRC, sequence
 * number, timestamps, MID — intact and readable. That is deliberate and not a leak: the media server needs those
 * fields to route packets, and SRTP already protects them per hop. The *payload* is what stops being readable at
 * the server, which is the claim this file exists to make true.
 *
 * Three details are load-bearing:
 *
 *  1. **A fresh random IV per frame.** Reusing a nonce under AES-GCM is catastrophic — it leaks the XOR of two
 *     plaintexts and destroys the authentication tag. Random 96-bit IVs are safe to about 2^32 frames under one
 *     key; a 50-seat room tops out near 1,350 frames a second, so a key would have to run for weeks.
 *  2. **A failed tag drops the frame and never throws.** A pipeline whose transform throws is torn down and never
 *     recovers, so a corrupt packet would end the call instead of costing one frame. The failure is counted and
 *     reported to the main thread, where the room can say something is wrong.
 *  3. **The key never leaves the worker.** It arrives once through `postMessage` and is imported as a non-extractable
 *     `CryptoKey`; nothing here can export it again.
 *
 * The room key is derived (HKDF-SHA256) from the key that already protects the whiteboard, which the host creates
 * in their browser and shares in the URL fragment — the one part of a link a browser never sends to a server. Audio,
 * video and the board are therefore end-to-end under one key, and the API has never seen it.
 *
 * This file is plain JavaScript with no imports because a worker is not bundled: it is served from `public/` as
 * written. `tests/media-e2ee.test.ts` loads its text and exercises the same functions the call path calls.
 */

/* eslint-disable no-restricted-globals */

const FRAME_VERSION = 0x01;
const IV_BYTES = 12;
const TAG_BITS = 128;
const KEY_BITS = 256;
/** How many failed frames accumulate before the main thread is told once. */
const FAILURE_REPORT_EVERY = 25;
const HKDF_SALT = "edumart/media/v1";
const HKDF_INFO = "rtp-frame";

/**
 * Derives the media key from the raw room key.
 *
 * HKDF rather than using the room key directly: the same bytes already encrypt whiteboard events under AES-GCM with
 * a 12-byte nonce, and two uses of one key with an independent nonce space is exactly the collision that turns a
 * working system into a broken one. Deriving a second key with a distinct salt and info keeps the nonce spaces
 * apart by construction.
 */
async function importRoomKey(cryptoApi, raw) {
  const base = await cryptoApi.subtle.importKey("raw", raw, "HKDF", false, ["deriveKey"]);
  return cryptoApi.subtle.deriveKey(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt: new TextEncoder().encode(HKDF_SALT),
      info: new TextEncoder().encode(HKDF_INFO),
    },
    base,
    { name: "AES-GCM", length: KEY_BITS },
    false,
    ["encrypt", "decrypt"],
  );
}

/** Seals one encoded frame. The returned buffer is a new allocation; the input is not modified. */
async function encryptFrame(cryptoApi, key, frame) {
  const iv = new Uint8Array(new ArrayBuffer(IV_BYTES));
  cryptoApi.getRandomValues(iv);
  const ciphertext = await cryptoApi.subtle.encrypt(
    { name: "AES-GCM", iv, tagLength: TAG_BITS },
    key,
    frame.data ?? frame,
  );
  const body = new Uint8Array(ciphertext);
  const out = new Uint8Array(new ArrayBuffer(1 + IV_BYTES + body.length));
  out[0] = FRAME_VERSION;
  out.set(iv, 1);
  out.set(body, 1 + IV_BYTES);
  return out;
}

/**
 * Opens one sealed frame, or returns null when it cannot be authenticated.
 *
 * Null rather than an exception: see the third header note. A frame sealed under another key, a truncated packet,
 * and a frame from a room this participant was not invited to all look the same from here — undecryptable — and all
 * three should cost exactly one frame.
 */
async function decryptFrame(cryptoApi, key, frame) {
  const bytes = frame.data ?? frame;
  if (!bytes || bytes.length <= 1 + IV_BYTES || bytes[0] !== FRAME_VERSION) return null;
  const iv = bytes.subarray(1, 1 + IV_BYTES);
  const body = bytes.subarray(1 + IV_BYTES);
  try {
    const plaintext = await cryptoApi.subtle.decrypt({ name: "AES-GCM", iv, tagLength: TAG_BITS }, key, body);
    return new Uint8Array(plaintext);
  } catch {
    return null;
  }
}

/**
 * One transform, for one direction, for the life of a connection.
 *
 * `transform` is the piped pair the browser hands over. Frames are processed strictly in order and awaited one at a
 * time: a transform that processed frames concurrently could emit them out of order, which for video means decoding
 * a P-frame before its keyframe.
 */
function pipe(transformer, cryptoApi, operation, derive) {
  const state = { key: null, failures: 0 };
  const ready = derive().then((key) => {
    state.key = key;
  });

  const pump = async () => {
    const reader = transformer.readable.getReader();
    const writer = transformer.writable.getWriter();
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        // A key that never arrived (a corrupt link, a browser that dropped the message) must cost frames, not the
        // pipeline: closing here would end audio and video for the rest of the call on a recoverable fault, and the
        // main thread would have nothing to report beyond "it stopped".
        try {
          await ready;
        } catch {
          state.failures += 1;
          continue;
        }
        try {
          if (operation === "encode") {
            writer.write(await encryptFrame(cryptoApi, state.key, value));
          } else {
            const opened = await decryptFrame(cryptoApi, state.key, value);
            if (opened === null) {
              state.failures += 1;
              if (state.failures % FAILURE_REPORT_EVERY === 0) {
                self.postMessage({ type: "decrypt-failed", count: state.failures });
              }
              continue;
            }
            writer.write(opened);
          }
        } catch (error) {
          // Writing to a closed stream is normal during teardown; anything else is reported rather than thrown,
          // because an exception here would kill the pipeline for the rest of the call.
          self.postMessage({ type: "transform-error", message: String(error && error.message) });
          break;
        }
      }
    } catch {
      // The readable closed under us: teardown, not failure.
    } finally {
      try {
        await reader.cancel();
      } catch {
        // already cancelled
      }
      try {
        await writer.close();
      } catch {
        // already closed
      }
    }
  };

  return { ready, pump, state };
}

/** The key for this worker, derived from the room key the main thread sent. */
let roomKeyPromise = null;
const cryptoApi = self.crypto || globalThis.crypto;

function deriveKey() {
  return roomKeyPromise;
}

self.onmessage = (event) => {
  const message = event.data || {};
  if (message.type !== "key" || !message.raw) return;
  // The main thread transferred the bytes; they are now this worker's alone.
  roomKeyPromise = importRoomKey(cryptoApi, message.raw);
  roomKeyPromise.catch((error) => {
    self.postMessage({ type: "key-error", message: String(error && error.message) });
  });
};

/**
 * The modern hook: the browser constructs a transform per sender/receiver and fires this event.
 *
 * `event.transformer` carries the readable/writable pair; `options.operation` is what the main thread passed to the
 * `RTCRtpScriptTransform` constructor ("encode" for a sender, "decode" for a receiver).
 */
self.onrtctransform = (event) => {
  const transformer = event.transformer;
  const operation = (transformer.options && transformer.options.operation) || "decode";
  const { pump } = pipe(transformer, cryptoApi, operation, deriveKey);
  void pump();
};

/**
 * The older hook: `createEncodedStreams()` on the main thread hands the streams over in a message. Kept because it is
 * what a Chromium build without `RTCRtpScriptTransform` provides, and because the main thread picks between the two
 * paths — this worker only has to accept whichever arrives.
 */
self.addEventListener("message", (event) => {
  const message = event.data || {};
  if (message.type !== "streams" || !message.readable || !message.writable) return;
  const transformer = { readable: message.readable, writable: message.writable, options: message.options || {} };
  const { pump } = pipe(transformer, cryptoApi, message.options?.operation ?? "decode", deriveKey);
  void pump();
});

// Test seam. `tests/media-e2ee.test.ts` evaluates this file and calls these directly, so the framing that the call
// path uses is the framing that is tested — there is no second implementation to drift.
self.__edumartMediaE2EE = { encryptFrame, decryptFrame, importRoomKey, FRAME_VERSION, IV_BYTES };
