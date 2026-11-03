import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

/**
 * The worker that encrypts media is served as written — it is not bundled, so nothing type-checks it and nothing
 * imports it. That is exactly how an encryption path rots: the app keeps calling it and the file keeps not being
 * tested. So the tests evaluate the real file and call the same functions the call path calls, which means there is
 * one implementation and it is the one under test.
 */

interface FrameCrypto {
  encryptFrame: (cryptoApi: Crypto, key: CryptoKey, frame: { data: Uint8Array }) => Promise<Uint8Array>;
  decryptFrame: (cryptoApi: Crypto, key: CryptoKey, frame: Uint8Array) => Promise<Uint8Array | null>;
  importRoomKey: (cryptoApi: Crypto, raw: Uint8Array) => Promise<CryptoKey>;
  FRAME_VERSION: number;
  IV_BYTES: number;
}

interface WorkerScope {
  onmessage: ((event: MessageEvent) => void) | null;
  onrtctransform: ((event: unknown) => void) | null;
  addEventListener: (type: string, listener: (event: MessageEvent) => void) => void;
  postMessage: (message: unknown) => void;
  crypto: Crypto;
  __edumartMediaE2EE: FrameCrypto;
}

const source = readFileSync(path.resolve(__dirname, "../public/media-e2ee-worker.js"), "utf8");

/**
 * Evaluates the worker file in this realm, with a `self` shaped like a DedicatedWorkerGlobalScope.
 *
 * `dispatch` delivers a message the way the browser does: to `onmessage` **and** to every `message` listener, which
 * is what the streams hook relies on (the key arrives on `onmessage`, the streams on a listener).
 */
function loadWorker(): { scope: WorkerScope; posted: unknown[]; dispatch: (data: unknown) => void } {
  const posted: unknown[] = [];
  const listeners: Array<(event: MessageEvent) => void> = [];
  const scope = {
    crypto: globalThis.crypto,
    postMessage: (message: unknown) => posted.push(message),
    addEventListener: (type: string, listener: (event: MessageEvent) => void) => {
      if (type === "message") listeners.push(listener);
    },
    onmessage: null as WorkerScope["onmessage"],
    onrtctransform: null as WorkerScope["onrtctransform"],
  };
  // The file is an IIFE-free script: it assigns onto `self`. Running it with a fake `self` is how a worker behaves.
  const run = new Function("self", "TextEncoder", "TransformStream", source);
  run(scope, TextEncoder, TransformStream);

  const dispatch = (data: unknown) => {
    const event = { data } as MessageEvent;
    scope.onmessage?.(event);
    for (const listener of listeners) listener(event);
  };
  return { scope: scope as unknown as WorkerScope, posted, dispatch };
}

function roomKey(): Uint8Array {
  const raw = new Uint8Array(new ArrayBuffer(32));
  globalThis.crypto.getRandomValues(raw);
  return raw;
}

function frame(payload: string): { data: Uint8Array } {
  return { data: new TextEncoder().encode(payload) };
}

describe("media end-to-end encryption", () => {
  it("round-trips a frame through encrypt and decrypt", async () => {
    const { scope } = loadWorker();
    const cryptoApi = scope.crypto;
    const key = await scope.__edumartMediaE2EE.importRoomKey(cryptoApi, roomKey());

    const plaintext = "an encoded video frame, which is plaintext bytes and nothing else";
    const sealed = await scope.__edumartMediaE2EE.encryptFrame(cryptoApi, key, frame(plaintext));

    // The frame really changed: a test that only checked the round trip would pass on a no-op cipher.
    expect(Buffer.from(sealed).includes(Buffer.from("encoded video frame"))).toBe(false);
    expect(sealed[0]).toBe(scope.__edumartMediaE2EE.FRAME_VERSION);

    const opened = await scope.__edumartMediaE2EE.decryptFrame(cryptoApi, key, sealed);
    expect(opened).not.toBeNull();
    expect(new TextDecoder().decode(opened!)).toBe(plaintext);
  });

  it("uses a fresh IV per frame, so two identical frames differ on the wire", async () => {
    const { scope } = loadWorker();
    const cryptoApi = scope.crypto;
    const key = await scope.__edumartMediaE2EE.importRoomKey(cryptoApi, roomKey());

    const first = await scope.__edumartMediaE2EE.encryptFrame(cryptoApi, key, frame("same frame"));
    const second = await scope.__edumartMediaE2EE.encryptFrame(cryptoApi, key, frame("same frame"));
    // Identical ciphertext would mean a reused nonce, which under AES-GCM leaks the XOR of the two plaintexts.
    expect(Buffer.from(first).equals(Buffer.from(second))).toBe(false);

    const ivLength = scope.__edumartMediaE2EE.IV_BYTES;
    const firstIv = Buffer.from(first.subarray(1, 1 + ivLength));
    const secondIv = Buffer.from(second.subarray(1, 1 + ivLength));
    expect(firstIv.equals(secondIv)).toBe(false);
  });

  it("derives a different key per room, so a frame from another class cannot be opened", async () => {
    const { scope } = loadWorker();
    const cryptoApi = scope.crypto;
    const ours = await scope.__edumartMediaE2EE.importRoomKey(cryptoApi, roomKey());
    const theirs = await scope.__edumartMediaE2EE.importRoomKey(cryptoApi, roomKey());

    const sealed = await scope.__edumartMediaE2EE.encryptFrame(cryptoApi, theirs, frame("not for you"));
    // Null, not an exception: a receiver counts the frame as lost and keeps the call up.
    await expect(scope.__edumartMediaE2EE.decryptFrame(cryptoApi, ours, sealed)).resolves.toBeNull();
  });

  it("refuses a tampered or truncated frame instead of decrypting garbage", async () => {
    const { scope } = loadWorker();
    const cryptoApi = scope.crypto;
    const key = await scope.__edumartMediaE2EE.importRoomKey(cryptoApi, roomKey());
    const sealed = await scope.__edumartMediaE2EE.encryptFrame(cryptoApi, key, frame("a whole frame"));

    const tampered = new Uint8Array(sealed);
    // Flip one ciphertext bit: the authentication tag is what makes this a refusal rather than a wrong picture.
    tampered[tampered.length - 1] = (tampered[tampered.length - 1] ?? 0) ^ 0xff;
    await expect(scope.__edumartMediaE2EE.decryptFrame(cryptoApi, key, tampered)).resolves.toBeNull();

    // A frame shorter than the header, and one with the wrong version byte, are both "not ours".
    await expect(scope.__edumartMediaE2EE.decryptFrame(cryptoApi, key, sealed.subarray(0, 8))).resolves.toBeNull();
    const wrongVersion = new Uint8Array(sealed);
    wrongVersion[0] = 0x02;
    await expect(scope.__edumartMediaE2EE.decryptFrame(cryptoApi, key, wrongVersion)).resolves.toBeNull();
  });

  it("imports the room key as a non-extractable key", async () => {
    const { scope } = loadWorker();
    const key = await scope.__edumartMediaE2EE.importRoomKey(scope.crypto, roomKey());
    // The page hands the room key to the worker so the page cannot use it; a media key that could be exported
    // would hand the same material back.
    expect(key.extractable).toBe(false);
    expect(key.algorithm).toMatchObject({ name: "AES-GCM", length: 256 });
    await expect(scope.crypto.subtle.exportKey("raw", key)).rejects.toBeTruthy();
  });

  it("carries a frame from one worker to another, and a frame it cannot open costs only that frame", async () => {
    // Reads one frame, or fails with what the worker said about itself. A bare timeout after a pipeline bug says
    // "something hung"; this says which line of the worker objected, which is the difference between a five-minute fix
    // and an afternoon.
    const nextFrame = async (
      reader: ReadableStreamDefaultReader<Uint8Array>,
      worker: { posted: unknown[] },
      what: string,
    ) => {
      const outcome = await Promise.race([
        reader.read().then((result) => ({ result })),
        new Promise<{ timeout: true }>((resolve) => setTimeout(() => resolve({ timeout: true }), 2000)),
      ]);
      if ("timeout" in outcome) {
        throw new Error(`${what}: no frame arrived. The worker reported: ${JSON.stringify(worker.posted)}`);
      }
      if (outcome.result.done) {
        throw new Error(`${what}: the pipeline closed. The worker reported: ${JSON.stringify(worker.posted)}`);
      }
      return outcome.result.value as Uint8Array;
    };

    // One room key, shared the way the room shares it: through the link, into both workers.
    const roomKeyBytes = roomKey();

    const sender = loadWorker();
    sender.dispatch({ type: "key", raw: roomKeyBytes.buffer.slice(0) });

    const inbound = new TransformStream();
    const outbound = new TransformStream();
    const write = inbound.writable.getWriter();
    const read = outbound.readable.getReader();
    sender.dispatch({
      type: "streams",
      readable: inbound.readable,
      writable: outbound.writable,
      options: { operation: "encode" },
    });

    await write.write(frame("frame one"));
    const sealed = await nextFrame(read, sender, "sender, first frame");
    // The frame left the sender encrypted: this is the property the media server depends on.
    expect(new TextDecoder().decode(sealed)).not.toContain("frame one");

    // A receiver holding the same room key opens it — the two independent worker instances agree on the framing.
    const receiver = loadWorker();
    receiver.dispatch({ type: "key", raw: roomKeyBytes.buffer.slice(0) });
    const decodeIn = new TransformStream();
    const decodeOut = new TransformStream();
    const writeSealed = decodeIn.writable.getWriter();
    const readPlain = decodeOut.readable.getReader();
    receiver.dispatch({
      type: "streams",
      readable: decodeIn.readable,
      writable: decodeOut.writable,
      options: { operation: "decode" },
    });

    await writeSealed.write(sealed);
    const opened = await nextFrame(readPlain, receiver, "receiver, a sealed frame");
    expect(new TextDecoder().decode(opened)).toBe("frame one");

    // A frame nobody sealed is dropped, and the pipeline keeps going: one lost frame, not a dead call.
    await writeSealed.write(frame("not a sealed frame at all").data);
    await write.write(frame("frame two"));
    await writeSealed.write(await nextFrame(read, sender, "sender, second frame"));
    const recovered = await nextFrame(readPlain, receiver, "receiver, after a dropped frame");
    expect(new TextDecoder().decode(recovered)).toBe("frame two");
  });
});
