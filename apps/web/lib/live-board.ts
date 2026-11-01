/**
 * The whiteboard's end-to-end encryption.
 *
 * A stroke is serialised, encrypted with AES-GCM under a room key, and only the nonce and ciphertext are sent to
 * the API — which stores and orders them so a late joiner can replay the board, and cannot read one. The key is
 * generated in the host's browser and handed to each participant over the WebRTC data channel, which is already
 * DTLS-encrypted and does not touch the API at all.
 *
 * That is the whole claim, and it is deliberately narrow: the *board* is end-to-end encrypted. Audio and video go
 * through the media forwarder, so they are encrypted hop by hop rather than end to end — see
 * docs/VIRTUAL_CLASSES.md, which says so rather than implying otherwise.
 */

export interface SealedBoardEvent {
  nonce: string;
  ciphertext: string;
}

export interface BoardKey {
  key: CryptoKey;
  /** The raw bytes, kept only in memory so the key can be handed to a peer that joins later. */
  raw: Uint8Array<ArrayBuffer>;
}

/**
 * Allocates bytes over a plain ArrayBuffer.
 *
 * `new Uint8Array(n)` is typed as `Uint8Array<ArrayBufferLike>`, which WebCrypto rejects as a BufferSource
 * because the backing store could be shared. Allocating the buffer explicitly is the difference between a
 * type error and a call that compiles.
 */
function bytes(length: number): Uint8Array<ArrayBuffer> {
  return new Uint8Array(new ArrayBuffer(length));
}

const ALGORITHM = "AES-GCM";
const KEY_BITS = 256;
const NONCE_BYTES = 12;
const TAG_BITS = 128;

function subtle(): SubtleCrypto {
  const crypto = globalThis.crypto;
  if (!crypto?.subtle) {
    throw new Error("This browser cannot encrypt the whiteboard (WebCrypto is unavailable over plain HTTP).");
  }
  return crypto.subtle;
}

export function toBase64Url(input: Uint8Array): string {
  let binary = "";
  for (const byte of input) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function fromBase64Url(value: string): Uint8Array<ArrayBuffer> {
  const normalised = value.replace(/-/g, "+").replace(/_/g, "/");
  const padded = normalised + "=".repeat((4 - (normalised.length % 4)) % 4);
  const binary = atob(padded);
  const out = bytes(binary.length);
  for (let index = 0; index < binary.length; index += 1) out[index] = binary.charCodeAt(index);
  return out;
}

/** Creates a fresh room key in this browser. Only the host does this, once per class. */
export async function createBoardKey(): Promise<BoardKey> {
  const raw = bytes(KEY_BITS / 8);
  globalThis.crypto.getRandomValues(raw);
  const key = await subtle().importKey("raw", raw, ALGORITHM, false, ["encrypt", "decrypt"]);
  return { key, raw };
}

/** Serialises a key for the data channel. It never goes to the API. */
export function exportBoardKey(boardKey: BoardKey): string {
  return toBase64Url(boardKey.raw);
}

/** Rebuilds a key a peer sent over the data channel. */
export async function importBoardKey(encoded: string): Promise<BoardKey> {
  const raw = fromBase64Url(encoded);
  if (raw.length !== KEY_BITS / 8) {
    throw new Error("That room key is the wrong length.");
  }
  const key = await subtle().importKey("raw", raw, ALGORITHM, false, ["encrypt", "decrypt"]);
  return { key, raw };
}

/** Encrypts one board event. A fresh nonce per event is what keeps two identical strokes two different payloads. */
export async function sealBoardEvent(boardKey: BoardKey, payload: unknown): Promise<SealedBoardEvent> {
  const nonce = bytes(NONCE_BYTES);
  globalThis.crypto.getRandomValues(nonce);
  const plaintext = new TextEncoder().encode(JSON.stringify(payload));
  const ciphertext = await subtle().encrypt(
    { name: ALGORITHM, iv: nonce, tagLength: TAG_BITS },
    boardKey.key,
    plaintext,
  );
  return {
    nonce: toBase64Url(nonce),
    ciphertext: toBase64Url(new Uint8Array(ciphertext)),
  };
}

/**
 * Decrypts one board event.
 *
 * It throws rather than returning null when the payload was sealed under another key, because the caller has to
 * decide what "I cannot read this" means; silently drawing nothing is how a board ends up looking empty to the
 * one person who cannot see it.
 */
export async function openBoardEvent<T>(boardKey: BoardKey, event: SealedBoardEvent): Promise<T> {
  const nonce = fromBase64Url(event.nonce);
  const ciphertext = fromBase64Url(event.ciphertext);
  const plaintext = await subtle().decrypt(
    { name: ALGORITHM, iv: nonce, tagLength: TAG_BITS },
    boardKey.key,
    ciphertext as unknown as BufferSource,
  );
  return JSON.parse(new TextDecoder().decode(plaintext)) as T;
}
