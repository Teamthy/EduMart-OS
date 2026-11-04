import { describe, expect, it } from "vitest";

import { mediaFrameCodec } from "@/lib/live-media-e2ee";
import { StageRecorder, createRecordingUploader, pickMimeType, recorderSupport } from "@/lib/live-recorder";

/** The room key, as the join link's fragment carries it. */
function roomKey(seed = 7): Uint8Array {
  const bytes = new Uint8Array(new ArrayBuffer(32));
  for (let index = 0; index < bytes.length; index++) bytes[index] = (index * seed + 11) & 0xff;
  return bytes;
}

/** A stream-shaped stub: the recorder only asks for its tracks. */
function fakeStream(trackCount = 2): MediaStream {
  const tracks = Array.from({ length: trackCount }, (_, index) => ({ kind: index === 0 ? "audio" : "video" }));
  return { getTracks: () => tracks, getAudioTracks: () => tracks.slice(0, 1) } as unknown as MediaStream;
}

describe("who ends up in the recording", () => {
  it("includes only the people who acknowledged the notice", () => {
    const streams = [
      { userId: "host", stream: fakeStream(), label: "Amara (host)" },
      { userId: "acknowledged", stream: fakeStream(), label: "Tunde" },
      { userId: "silent", stream: fakeStream(), label: "Nobody asked me" },
    ];

    const included = StageRecorder.includeOnly(streams, ["host", "acknowledged"]);

    // This is the consent control: somebody who did not acknowledge is not merely unlisted, they are not drawn, so
    // they are not in the file the server stores.
    expect(included.map((entry) => entry.userId)).toEqual(["host", "acknowledged"]);
    expect(included.some((entry) => entry.userId === "silent")).toBe(false);
  });

  it("drops a participant whose stream has ended", () => {
    const streams = [{ userId: "gone", stream: fakeStream(0), label: "Left" }];
    expect(StageRecorder.includeOnly(streams, ["gone"])).toHaveLength(0);
  });

  it("includes nobody when nobody acknowledged", () => {
    const streams = [{ userId: "host", stream: fakeStream(), label: "Amara" }];
    expect(StageRecorder.includeOnly(streams, [])).toHaveLength(0);
  });
});

describe("sealing a recording chunk", () => {
  it("round-trips a chunk and leaves the bytes unreadable on the way", async () => {
    const codec = await mediaFrameCodec(roomKey());
    const chunk = new TextEncoder().encode("WEBM binary payload with a frame in it");

    const sealed = await codec.seal(chunk);

    // The layout is the same one the worker uses, which is the point: one format, one parser.
    expect(sealed[0]).toBe(0x01);
    expect(sealed.byteLength).toBe(1 + 12 + chunk.byteLength + 16);
    // What the server's disk holds is not the file.
    expect(new TextDecoder().decode(sealed)).not.toContain("WEBM binary payload");

    const opened = await codec.open(sealed);
    expect(opened).not.toBeNull();
    expect(new TextDecoder().decode(opened as Uint8Array)).toBe("WEBM binary payload with a frame in it");
  });

  it("seals two identical chunks differently", async () => {
    const codec = await mediaFrameCodec(roomKey());
    const chunk = new TextEncoder().encode("the same four seconds twice");
    const first = await codec.seal(chunk);
    const second = await codec.seal(chunk);
    // A fresh IV every time: the same plaintext must not produce the same ciphertext, or the IV is not random.
    expect(Array.from(first)).not.toEqual(Array.from(second));
  });

  it("refuses a chunk sealed under another room's key", async () => {
    const mine = await mediaFrameCodec(roomKey(7));
    const theirs = await mediaFrameCodec(roomKey(9));
    const sealed = await theirs.seal(new TextEncoder().encode("a different class"));
    // Null, not an exception: an unreadable chunk is a missing part, not a crash in the player.
    expect(await mine.open(sealed)).toBeNull();
  });

  it("refuses a tampered chunk and a chunk that is not sealed at all", async () => {
    const codec = await mediaFrameCodec(roomKey());
    const sealed = await codec.seal(new TextEncoder().encode("a whole chunk"));
    const tampered = new Uint8Array(sealed);
    tampered[tampered.length - 1] = (tampered[tampered.length - 1] ?? 0) ^ 0xff;
    expect(await codec.open(tampered)).toBeNull();
    expect(await codec.open(new TextEncoder().encode("plain webm"))).toBeNull();
    expect(await codec.open(new Uint8Array(4))).toBeNull();
  });
});

describe("what the browser can do", () => {
  it("names the missing capability instead of claiming recording works", () => {
    // jsdom has no MediaRecorder, so this is the honest answer here — and it must be a reason, not a bare false.
    const support = recorderSupport();
    expect(support.supported).toBe(false);
    expect(support.reason.length).toBeGreaterThan(0);
  });

  it("returns no mime type rather than guessing one", () => {
    expect(pickMimeType()).toBeNull();
  });
});

describe("the uploader", () => {
  it("posts the sealed bytes as octet-stream with the sequence in the query", async () => {
    const calls: Array<{ url: string; init: RequestInit }> = [];
    const fetchImpl = (async (url: string, init: RequestInit) => {
      calls.push({ url, init });
      return new Response("{}", { status: 201 });
    }) as unknown as typeof fetch;

    const uploader = createRecordingUploader({ classId: "class-1", fetchImpl });
    await uploader.put(3, new Uint8Array([1, 2, 3]), 4000);

    expect(calls[0]?.url).toBe("/v1/virtual-classes/class-1/recording/segments?seq=3&durationMs=4000");
    expect(calls[0]?.init.method).toBe("POST");
    expect((calls[0]?.init.headers as Record<string, string>)["Content-Type"]).toBe("application/octet-stream");
    expect(calls[0]?.init.credentials).toBe("include");
  });

  it("surfaces a rejected chunk so the recorder can retry it", async () => {
    const fetchImpl = (async () =>
      new Response(JSON.stringify({ error: { code: "PAYLOAD_TOO_LARGE", message: "too big" } }), {
        status: 413,
      })) as unknown as typeof fetch;
    const uploader = createRecordingUploader({ classId: "class-1", fetchImpl });
    await expect(uploader.put(1, new Uint8Array([1]), 100)).rejects.toThrow(/413/);
  });

  it("reports an expired session rather than silently failing every chunk", async () => {
    let unauthorised = false;
    const fetchImpl = (async () => new Response("{}", { status: 401 })) as unknown as typeof fetch;
    const uploader = createRecordingUploader({
      classId: "class-1",
      fetchImpl,
      onUnauthorised: () => {
        unauthorised = true;
      },
    });
    await expect(uploader.put(1, new Uint8Array([1]), 100)).rejects.toThrow();
    expect(unauthorised).toBe(true);
  });
});
