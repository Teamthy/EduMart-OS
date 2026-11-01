import type { Metadata } from "next";
import Link from "next/link";
import { Radio, Video } from "lucide-react";

import { formatDateTimeSafe } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { HostClassForm } from "./host-form";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";

export const metadata: Metadata = { title: "Live classes" };
export const dynamic = "force-dynamic";

interface VirtualClass {
  id: string;
  slug: string;
  title: string;
  creditsCost: number;
  status: string;
  startsAt: string;
  attendees: number;
  hostName: string;
  role: string;
  joinUrl: string;
}

export default async function LiveClassesPage() {
  await requireUser("/live");

  let classes: VirtualClass[] = [];
  try {
    ({ classes } = await serverFetch<{ classes: VirtualClass[] }>("/v1/virtual-classes?scope=all"));
  } catch (error) {
    if (!(error instanceof ServerApiError)) throw error;
    return <Alert tone="error" title="We couldn't load your live classes">{error.message}</Alert>;
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Live classes</h1>
        <p className="text-sm text-muted-foreground">
          Schedule a room, share the link, and teach with encrypted video, audio and a whiteboard. Any account can
          host — a student running revision is as welcome as an institution running a lecture.
        </p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Host a class</CardTitle>
        </CardHeader>
        <CardContent>
          <HostClassForm />
        </CardContent>
      </Card>

      {classes.length === 0 ? (
        <EmptyState
          title="No live classes yet"
          description="Schedule one above and you get a shareable link. Learners join with credits or a discount code."
          icon={<Video aria-hidden="true" className="size-6" />}
        />
      ) : (
        <ul className="space-y-3">
          {classes.map((item) => (
            <li key={item.id}>
              <Card>
                <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <p className="truncate font-semibold">{item.title}</p>
                      {item.role === "host" ? <Badge variant="default">you host</Badge> : null}
                      <Badge variant={item.status === "live" ? "success" : "outline"}>{item.status}</Badge>
                      <Badge variant="outline">
                        {item.creditsCost > 0 ? `${item.creditsCost} credits` : "free"}
                      </Badge>
                    </div>
                    <p className="text-xs text-muted-foreground">
                      {formatDateTimeSafe(item.startsAt)} · {item.attendees} seated · host {item.hostName}
                    </p>
                  </div>
                  <Button asChild size="sm" variant="outline">
                    <Link href={`/live/${item.slug}`}>
                      <Radio aria-hidden="true" className="size-4" />
                      Open room
                    </Link>
                  </Button>
                </CardContent>
              </Card>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
