import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft } from "lucide-react";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import type { LiveClass, RecordingDetail } from "@/lib/live-api";
import { RecordingPlayer } from "./player";

export const metadata: Metadata = { title: "Class recording" };
export const dynamic = "force-dynamic";

/**
 * The recording of one class.
 *
 * Everybody who was in the class can open this, which is deliberate: a recording of a class is part of that class, and
 * hiding it from the people who were in it would be strange. A stranger with the class's public slug gets a 403 from
 * the API, and this page shows that refusal rather than an empty player.
 *
 * The bytes on the server are ciphertext, sealed in the host's browser with the room key. That key lives in the join
 * link's URL fragment — the one part of a URL a browser never sends to a server — so this page cannot decrypt anything
 * without the link the class was shared with.
 */
export default async function RecordingPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const user = await requireUser(`/live/${slug}/recording`);

  let room: LiveClass;
  try {
    ({ class: room } = await serverFetch<{ class: LiveClass }>(`/v1/virtual-classes/${encodeURIComponent(slug)}`));
  } catch (error) {
    return (
      <main className="mx-auto max-w-3xl space-y-4 p-4 sm:p-6">
        <Alert tone="error" title="We couldn't open that class">
          {error instanceof ServerApiError ? error.message : "Please try again."}
        </Alert>
      </main>
    );
  }

  let recording: RecordingDetail | null = null;
  let failure: string | null = null;
  try {
    ({ recording } = await serverFetch<{ recording: RecordingDetail }>(
      `/v1/virtual-classes/${room.id}/recording`,
    ));
  } catch (error) {
    // "Not recorded" and "not yours to watch" are different answers, and the page must not flatten them: one is a
    // fact about the class, the other is a fact about the viewer.
    if (error instanceof ServerApiError) {
      failure = error.isForbidden
        ? "Only people who were in this class can watch its recording."
        : error.isNotFound
          ? "This class has no recording. A host can start one while the class is live."
          : error.message;
    } else {
      failure = "The recording could not be loaded.";
    }
  }

  return (
    <main className="mx-auto max-w-3xl space-y-4 p-4 sm:p-6">
      <div className="flex items-center justify-between">
        <Button asChild variant="ghost" size="sm">
          <Link href={`/live/${room.slug}`}>
            <ArrowLeft aria-hidden="true" className="size-4" />
            Back to the class
          </Link>
        </Button>
        <p className="text-xs text-muted-foreground">Signed in as {user.email}</p>
      </div>

      <header className="space-y-1">
        <h1 className="text-2xl">{room.title}</h1>
        <p className="text-sm text-muted-foreground">Recording · hosted by {room.hostName}</p>
      </header>

      {failure ? (
        <Alert tone="error" title="No recording to show">
          {failure}
        </Alert>
      ) : recording ? (
        <RecordingPlayer
          classId={room.id}
          recording={recording}
          isHost={room.role === "host"}
        />
      ) : null}
    </main>
  );
}
