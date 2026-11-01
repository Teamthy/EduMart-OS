import { describe, expect, it } from "vitest";

import {
  createBoardKey,
  exportBoardKey,
  fromBase64Url,
  importBoardKey,
  openBoardEvent,
  sealBoardEvent,
  toBase64Url,
} from "@/lib/live-board";

/**
 * The whiteboard's encryption, tested for the two properties the product claims: the server cannot read an
 * event, and a second participant holding the same key can.
 */
describe("live board cryptography", () => {
  it("round-trips base64url without losing bytes", () => {
    const bytes = new Uint8Array([0, 1, 2, 250, 251, 252, 253, 254, 255]);
    const encoded = toBase64Url(bytes);
    expect(encoded).not.toMatch(/[+/=]/);
    expect(Array.from(fromBase64Url(encoded))).toEqual(Array.from(bytes));
  });

  it("seals an event so only the room key opens it", async () => {
    const key = await createBoardKey();
    const stroke = { tool: "pen", color: "#1d4ed8", width: 3, points: [{ x: 1, y: 2 }] };

    const sealed = await sealBoardEvent(key, stroke);
    // What the API stores is opaque: no JSON, no coordinates, no colour.
    expect(sealed.nonce).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(sealed.ciphertext).toMatch(/^[A-Za-z0-9_-]+$/);
    expect(sealed.ciphertext).not.toContain("1d4ed8");

    await expect(openBoardEvent(key, sealed)).resolves.toEqual(stroke);
  });

  it("produces different ciphertext for identical strokes", async () => {
    const key = await createBoardKey();
    const stroke = { tool: "pen", points: [{ x: 0, y: 0 }] };
    const first = await sealBoardEvent(key, stroke);
    const second = await sealBoardEvent(key, stroke);

    // A fresh IV per event: two identical strokes must not be linkable by an observer holding the ciphertext.
    expect(first.nonce).not.toEqual(second.nonce);
    expect(first.ciphertext).not.toEqual(second.ciphertext);
  });

  it("refuses an event sealed under a different room key", async () => {
    const host = await createBoardKey();
    const stranger = await createBoardKey();
    const sealed = await sealBoardEvent(host, { tool: "pen", points: [] });

    // A participant in another room cannot decrypt this board, and a failure is the correct answer.
    await expect(openBoardEvent(stranger, sealed)).rejects.toBeTruthy();
  });

  it("carries the key between participants through an export/import round trip", async () => {
    const host = await createBoardKey();
    const exported = await exportBoardKey(host);
    const guest = await importBoardKey(exported);

    const sealed = await sealBoardEvent(host, { tool: "eraser", points: [{ x: 5, y: 5 }] });
    await expect(openBoardEvent(guest, sealed)).resolves.toEqual({ tool: "eraser", points: [{ x: 5, y: 5 }] });
  });
});
