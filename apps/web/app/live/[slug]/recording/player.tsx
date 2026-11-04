"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { Download, Loader2, Lock, Trash2 } from "lucide-react";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { deleteRecording, recordingSegmentUrl, type RecordingDetail } from "@/lib/live-api";
import { importBoardKey } from "@/lib/live-board";
import { mediaFrameCodec } from "@/lib/live-media-e2ee";

/**
 * Plays a recording, and is the only place it is ever decrypted.
 *
 * The server holds ciphertext: sealed in the host's browser with the room key, which travels in the join link's URL
 * fragment and never reaches the API. So playback is: fetch each chunk with the session cookie, open it with the key
 * from the fragment, concatenate, and hand the browser a blob URL.
 *
 * Three things are deliberate:
 *
 *  - **No key, no playback.** A viewer who opened this page without the class link is told so, and nothing is guessed
 *    at or half-shown. An empty player would be indistinguishable from a broken recording.
 *  - **A missing chunk is announced.** The server reports gaps in the recorder's sequence; the player says how many
 *    parts are missing instead of playing a file with a silent hole in it.
 *  - **The whole file is assembled before playing.** Chunks of one MediaRecorder session concatenate into one container,
 *    and a `MediaSource` pipeline for WebM is fiddly enough to break on exactly the browsers least able to afford it.
 *    A class is tens of megabytes; assembling it is a wait of seconds, not minutes.
 */
export function RecordingPlayer({
  classId,
  recording,
  isHost,
}: {
  classId: string;
  recording: RecordingDetail;
  isHost: boolean;
}) {
  const [state, setState] = useState<"idle" | "loading" | "ready" | "error">("idle");
  const [progress, setProgress] = useState({ loaded: 0, total: 0 });
  const [message, setMessage] = useState<string | null>(null);
  const [url, setUrl] = useState<string | null>(null);
  const [missing, setMissing] = useState<number[]>(recording.missingSeqs ?? []);
  const [removing, setRemoving] = useState(false);
  const urlRef = useRef<string | null>(null);

  // A blob URL is a handle on memory the browser will not free on its own; releasing it on unmount is the difference
  // between a long session and a tab that grows until it dies.
  useEffect(
    () => () => {
      if (urlRef.current) URL.revokeObjectURL(urlRef.current);
    },
    [],
  );

  const load = useCallback(async () => {
    setState("loading");
    setMessage(null);
    setProgress({ loaded: 0, total: recording.segments.length });

    const fragment = typeof window === "undefined" ? "" : window.location.hash.replace(/^#/, "");
    const rawKey = new URLSearchParams(fragment).get("k");
    if (!rawKey) {
      setState("error");
      setMessage(
        "This page has no class key, so the recording stays sealed. Open the join link the host sent — the key is in the part of it after the #.",
      );
      return;
    }

    let key: Uint8Array;
    try {
      // The recording is sealed with the same derivation the call used, so the same key opens both.
      key = (await importBoardKey(rawKey)).raw;
    } catch {
      setState("error");
      setMessage("The key in this link is not usable. Ask the host for the class link again.");
      return;
    }
    const codec = await mediaFrameCodec(key);

    const parts: Uint8Array[] = [];
    let failed = 0;
    for (const segment of recording.segments) {
      try {
        const response = await fetch(recordingSegmentUrl(classId, segment.id), { credentials: "include" });
        if (!response.ok) throw new Error(String(response.status));
        const sealed = new Uint8Array(await response.arrayBuffer());
        const opened = await codec.open(sealed);
        if (!opened) {
          // A chunk that will not open is a hole, and holes are counted rather than skipped in silence.
          failed += 1;
        } else {
          parts.push(opened);
        }
      } catch {
        failed += 1;
      }
      setProgress((current) => ({ ...current, loaded: current.loaded + 1 }));
    }

    if (missing.length === 0 && failed > 0) {
      setMissing(Array.from({ length: failed }, (_, index) => index + 1));
    }

    if (parts.length === 0) {
      setState("error");
      setMessage(
        failed > 0
          ? "None of this recording could be opened with the key in this link. It may belong to a different class."
          : "This recording is empty.",
      );
      return;
    }

    const blob = new Blob(parts.map((part) => new Uint8Array(part)), { type: "video/webm" });
    if (urlRef.current) URL.revokeObjectURL(urlRef.current);
    const objectUrl = URL.createObjectURL(blob);
    urlRef.current = objectUrl;
    setUrl(objectUrl);
    setState("ready");
  }, [classId, recording.segments, missing.length]);

  const erase = useCallback(async () => {
    // Deleting is irreversible and it is other people's class, so the confirmation is a real one rather than a toast
    // that cannot be taken back.
    if (!window.confirm("Delete this recording for everyone? The bytes are erased, not hidden.")) return;
    setRemoving(true);
    try {
      await deleteRecording(classId);
      setMessage("The recording is deleted and its bytes are erased.");
      setState("error");
      if (urlRef.current) URL.revokeObjectURL(urlRef.current);
      setUrl(null);
    } catch (caught) {
      setMessage(caught instanceof Error ? caught.message : "The recording could not be deleted.");
    } finally {
      setRemoving(false);
    }
  }, [classId]);

  const sizeMB = (recording.bytes / (1024 * 1024)).toFixed(1);
  const minutes = Math.max(1, Math.round(recording.durationMs / 60000));

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle className="flex flex-wrap items-center gap-2">
            Recording
            <Badge variant={recording.status === "ready" ? "success" : recording.status === "failed" ? "destructive" : "outline"}>
              {recording.status}
            </Badge>
            <Badge variant="outline">
              <Lock aria-hidden="true" className="mr-1 size-3" />
              sealed on the server
            </Badge>
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 text-sm">
          <p className="text-muted-foreground">
            {recording.chunks} part(s) · {sizeMB} MB · about {minutes} min · kept until{" "}
            {new Date(recording.retainUntil).toLocaleDateString()}
          </p>

          {recording.failureReason ? (
            <Alert tone="error" title="This recording did not complete">
              {recording.failureReason}
            </Alert>
          ) : null}

          {missing.length > 0 ? (
            <Alert tone="info" title="This recording has gaps">
              {missing.length} part(s) never reached the server, so the recording skips where they would have been.
              Everything else plays.
            </Alert>
          ) : null}

          {message ? (
            <Alert tone={state === "error" ? "error" : "success"}>{message}</Alert>
          ) : null}

          {state === "ready" && url ? (
            <>
              <video src={url} controls playsInline className="w-full rounded-lg bg-black" />
              <div className="flex flex-wrap gap-2">
                <Button asChild variant="outline" size="sm">
                  <a href={url} download={`${classId}.webm`}>
                    <Download aria-hidden="true" className="size-4" />
                    Save the file
                  </a>
                </Button>
                {isHost ? (
                  <Button type="button" variant="destructive" size="sm" onClick={() => void erase()} disabled={removing}>
                    <Trash2 aria-hidden="true" className="size-4" />
                    Delete for everyone
                  </Button>
                ) : null}
              </div>
            </>
          ) : state === "loading" ? (
            <p className="flex items-center gap-2 text-muted-foreground">
              <Loader2 aria-hidden="true" className="size-4 animate-spin" />
              Opening the recording in this browser… {progress.loaded}/{progress.total} parts
            </p>
          ) : state === "idle" ? (
            <div className="space-y-2">
              <Button type="button" onClick={() => void load()} disabled={recording.segments.length === 0}>
                {recording.segments.length === 0 ? "Nothing recorded to play" : "Play the recording"}
              </Button>
              <p className="text-xs text-muted-foreground">
                The file is decrypted here, in your browser, with the class key. The server only ever stored
                ciphertext.
              </p>
            </div>
          ) : null}

          <p className="text-xs text-muted-foreground">
            Captured in a participant&apos;s browser, sealed before it left ·{" "}
            {recording.acknowledgedUserIds.length} participant(s) acknowledged the recording notice · deleting erases the
            bytes immediately.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
