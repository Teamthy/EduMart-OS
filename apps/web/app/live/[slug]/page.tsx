import type { Metadata } from "next";
import Link from "next/link";
import { CalendarClock, Lock, Users } from "lucide-react";

import { formatDateTimeSafe } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { getCurrentUser } from "@/lib/session";
import { LiveRoom, type RoomClass } from "./room";

export const metadata: Metadata = { title: "Live class" };
export const dynamic = "force-dynamic";

interface Invite {
  id: string;
  slug: string;
  title: string;
  description: string;
  hostName: string;
  startsAt: string;
  creditsCost: number;
  status: string;
  joinUrl: string;
}

/**
 * The shareable link's landing page.
 *
 * It is deliberately **not** behind the authenticated shell: a link that demands an account before saying what
 * it is for is a link nobody opens twice. A visitor sees the class, the host and the price; the room itself is
 * only rendered for someone who is signed in, because joining has an economic consequence.
 */
export default async function JoinPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;

  let invite: Invite;
  try {
    ({ class: invite } = await serverFetch<{ class: Invite }>(`/v1/join/${encodeURIComponent(slug)}`));
  } catch (error) {
    if (error instanceof ServerApiError && error.isNotFound) {
      return (
        <main className="mx-auto max-w-2xl p-6">
          <Alert tone="error" title="That link does not lead anywhere">
            The class may have been removed, or the link was typed with a character missing.
          </Alert>
        </main>
      );
    }
    return (
      <main className="mx-auto max-w-2xl p-6">
        <Alert tone="error" title="We couldn't open this class">
          {error instanceof ServerApiError ? error.message : "Please try again."}
        </Alert>
      </main>
    );
  }

  const user = await getCurrentUser();
  let room: RoomClass | null = null;
  if (user) {
    try {
      // Signed-in visitors get the full record, which is what tells the room whether they are the host.
      ({ class: room } = await serverFetch<{ class: RoomClass }>(`/v1/virtual-classes/${invite.id}`));
    } catch {
      room = null;
    }
  }

  return (
    <main className="mx-auto max-w-4xl space-y-4 p-4 sm:p-6">
      <header className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={invite.status === "live" ? "success" : "outline"}>{invite.status}</Badge>
          <Badge variant="outline">{invite.creditsCost > 0 ? `${invite.creditsCost} credits` : "free"}</Badge>
          <Badge variant="outline">
            <Lock aria-hidden="true" className="mr-1 size-3" />
            end-to-end encrypted
          </Badge>
        </div>
        <h1 className="text-2xl">{invite.title}</h1>
        <p className="text-sm text-muted-foreground">
          Hosted by {invite.hostName} · {formatDateTimeSafe(invite.startsAt)}
        </p>
        {invite.description ? <p className="text-sm">{invite.description}</p> : null}
      </header>

      {room ? (
        <LiveRoom room={room} />
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>Sign in to take your seat</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-sm text-muted-foreground">
              {invite.creditsCost > 0
                ? `Seats are paid for with learning credits (${invite.creditsCost} for this class), or a discount code from the host.`
                : "This class is free — signing in is how the host knows who is in the room."}
            </p>
            <div className="flex flex-wrap gap-2">
              <Button asChild>
                <Link href={`/login?next=${encodeURIComponent(`/live/${invite.slug}`)}`}>Sign in to join</Link>
              </Button>
              <Button asChild variant="outline">
                <Link href={`/register?ref=&next=${encodeURIComponent(`/live/${invite.slug}`)}`}>Create an account</Link>
              </Button>
            </div>
            <p className="text-xs text-muted-foreground">
              <Users aria-hidden="true" className="mr-1 inline size-3" />
              The attendee list is only visible inside the room.
            </p>
          </CardContent>
        </Card>
      )}

      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <CalendarClock aria-hidden="true" className="size-3" />
        Audio and video travel directly between participants. The server relays introductions, not media.
      </p>
    </main>
  );
}
