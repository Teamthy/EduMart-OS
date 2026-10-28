import type { Metadata } from "next";
import Link from "next/link";
import { notFound, redirect } from "next/navigation";
import { CheckCircle2, Clock } from "lucide-react";

import { formatMoney, formatRelativeDate, type Order } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { PendingPayment } from "./pending-payment";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";

export const metadata: Metadata = { title: "Checkout" };
export const dynamic = "force-dynamic";

const SANDBOX_ENABLED = process.env.EDUMART_ALLOW_SANDBOX_PAYMENTS !== "false";

export default async function CheckoutPage({ params }: { params: Promise<{ orderId: string }> }) {
  await requireUser();
  const { orderId } = await params;

  let order: Order;
  try {
    ({ order } = await serverFetch<{ order: Order }>(`/v1/orders/${orderId}`));
  } catch (error) {
    if (error instanceof ServerApiError && error.isNotFound) notFound();
    if (error instanceof ServerApiError && error.isForbidden) redirect("/permission-denied");
    return (
      <Alert tone="error" title="We couldn't load that order">
        {error instanceof ServerApiError ? error.message : "Please try again."}
      </Alert>
    );
  }

  const paid = order.status === "paid";

  return (
    <div className="mx-auto max-w-xl space-y-5">
      <nav aria-label="Breadcrumb" className="text-sm text-muted-foreground">
        <Link href="/store" className="hover:underline">
          Store
        </Link>{" "}
        / <span aria-current="page">Order</span>
      </nav>

      <Card>
        <CardHeader className="flex-row items-center justify-between gap-3">
          <CardTitle>Order {order.reference}</CardTitle>
          <Badge variant={paid ? "success" : "warning"}>{paid ? "Paid" : "Awaiting payment"}</Badge>
        </CardHeader>
        <CardContent className="space-y-4">
          <ul className="divide-y divide-border text-sm">
            {order.items.map((item) => (
              <li key={item.productId} className="flex items-center justify-between gap-3 py-2 first:pt-0">
                <span>{item.title}</span>
                <span className="text-muted-foreground">
                  {formatMoney(item.unitAmountMinor, order.currency)} × {item.quantity}
                </span>
              </li>
            ))}
          </ul>

          <dl className="space-y-1 border-t border-border pt-3 text-sm">
            <div className="flex justify-between">
              <dt className="text-muted-foreground">Subtotal</dt>
              <dd>{formatMoney(order.subtotalMinor, order.currency)}</dd>
            </div>
            {order.discountMinor > 0 ? (
              <div className="flex justify-between">
                <dt className="text-muted-foreground">Discount</dt>
                <dd>−{formatMoney(order.discountMinor, order.currency)}</dd>
              </div>
            ) : null}
            <div className="flex justify-between font-semibold">
              <dt>Total</dt>
              <dd>{formatMoney(order.totalMinor, order.currency)}</dd>
            </div>
          </dl>

          <p className="text-xs text-muted-foreground">Created {formatRelativeDate(order.createdAt)}</p>
        </CardContent>
      </Card>

      {paid ? (
        <Card className="border-[#bfe3cd] bg-[#eaf7ef]">
          <CardContent className="space-y-3 p-5">
            <p className="flex items-center gap-2 font-semibold text-[#0b5837]">
              <CheckCircle2 aria-hidden="true" className="size-5" />
              Payment confirmed
            </p>
            <p className="text-sm text-[#0b5837]">
              Your access was granted by the verified payment webhook{order.paidAt ? ` at ${formatRelativeDate(order.paidAt)}` : ""}.
              The item is now in your library.
            </p>
            <Button asChild>
              <Link href="/library">Open library</Link>
            </Button>
          </CardContent>
        </Card>
      ) : (
        <Card>
          <CardContent className="space-y-3 p-5">
            <p className="flex items-center gap-2 font-semibold">
              <Clock aria-hidden="true" className="size-4 text-warning" />
              Waiting for the provider to confirm
            </p>
            <p className="text-sm text-muted-foreground">
              This page refreshes from the server. Access is only granted when our provider sends a
              signed webhook matching this order&apos;s amount — the browser cannot mark an order paid.
            </p>
            <PendingPayment orderId={order.id} sandboxEnabled={SANDBOX_ENABLED} />
          </CardContent>
        </Card>
      )}
    </div>
  );
}
