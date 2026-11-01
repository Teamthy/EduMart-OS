"use client";

/**
 * The whiteboard's cryptography.
 *
 * The board is end-to-end encrypted: a room key is generated in the host's browser, shared with the other
 * participants over the WebRTC data channel (which is itself encrypted), and used to seal every stroke before
 * it leaves the page. The API stores and orders ciphertext and cannot decrypt a stroke, which is the whole
 * reason this file exists rather than a plaintext column.
 *
 * AES-GCM is the primitive. Each event carries a fresh random 12-byte IV, so two identical strokes never
 * produce identical ciphertext and a replay cannot be swapped for another.
 */

const KEY_BYTES = 32;
const IV_BYTES = 12;

export type BoardKey = CryptoKey;

function subtle(): SubtleCrypto {
  const impl = globalThis.crypto?.subtle;
  if (!impl) throw new Error("This browser cannot encrypt the whiteboard (no WebCrypto).");
  return impl;
}

export function toBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function fromBase64Url(value: string): Uint8Array<ArrayBuffer> {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  // The buffer is allocated explicitly so the result is a plain ArrayBuffer view: WebCrypto's types reject a
  // view that might be backed by a SharedArrayBuffer.
  const bytes = new Uint8Array(new ArrayBuffer(binary.length));
  for (let index = 0; index < binary.length; index += 1) bytes[index] = binary.charCodeAt(index);
  return bytes;
}

/** Generates a room key. Only the host does this; everybody else receives it over the data channel. */
export async function createBoardKey(): Promise<BoardKey> {
  const raw = new Uint8Array(new ArrayBuffer(KEY_BYTES));
  globalThis.crypto.getRandomValues(raw);
  return subtle().importKey("raw", raw, "AES-GCM", true, ["encrypt", "decrypt"]);
}

/** Exports a room key for transport. The exported bytes only ever travel inside a DTLS-protected channel. */
export async function exportBoardKey(key: BoardKey): Promise<string> {
  const raw = await subtle().exportKey("raw", key);
  return toBase64Url(new Uint8Array(raw));
}

export async function importBoardKey(encoded: string): Promise<BoardKey> {
  return subtle().importKey("raw", fromBase64Url(encoded), "AES-GCM", true, ["encrypt", "decrypt"]);
}

export interface SealedBoardEvent {
  nonce: string;
  ciphertext: string;
}

/** Seals one board event. Returns base64url pieces that the API accepts without ever seeing the plaintext. */
export async function sealBoardEvent(key: BoardKey, event: unknown): Promise<SealedBoardEvent> {
  const iv = new Uint8Array(new ArrayBuffer(IV_BYTES));
  globalThis.crypto.getRandomValues(iv);
  const plaintext = new TextEncoder().encode(JSON.stringify(event));
  const sealed = new Uint8Array(await subtle().encrypt({ name: "AES-GCM", iv }, key, plaintext));
  // The IV is stored alongside the ciphertext: it is not secret, and a missing IV makes the event unreadable
  // rather than merely unverifiable.
  return {
    nonce: toBase64Url(iv),
    ciphertext: toBase64Url(sealed),
  };
}

/** Opens a board event. Throws when this key is not the room's — which is the correct answer, not an error to hide. */
export async function openBoardEvent<T = unknown>(key: BoardKey, event: SealedBoardEvent): Promise<T> {
  const iv = fromBase64Url(event.nonce);
  const sealed = fromBase64Url(event.ciphertext);
  const plaintext = await subtle().decrypt({ name: "AES-GCM", iv }, key, sealed);
  return JSON.parse(new TextDecoder().decode(plaintext)) as T;
}
