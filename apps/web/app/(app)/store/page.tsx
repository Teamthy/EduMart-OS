import type { Metadata } from "next";
import Link from "next/link";
import { BookOpen } from "lucide-react";

import { coverTones, formatMoney, type Product } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { BuyButton } from "./buy-button";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";

export const metadata: Metadata = { title: "Store" };
export const dynamic = "force-dynamic";

export default async function StorePage() {
  await requireUser("/store");

  let products: Product[] = [];
  try {
    ({ products } = await serverFetch<{ products: Product[] }>("/v1/products"));
  } catch (error) {
    if (!(error instanceof ServerApiError)) throw error;
    return <Alert tone="error" title="The store is unavailable">{error.message}</Alert>;
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Store</h1>
        <p className="text-sm text-muted-foreground">
          Revision packs and past-paper books from verified publishers. Payment is confirmed by our
          provider&apos;s signed webhook — your access appears as soon as that lands.
        </p>
      </header>

      {products.length === 0 ? (
        <EmptyState
          title="Nothing in the store yet"
          description="Published titles appear here. Check back after the next content release."
          icon={<BookOpen aria-hidden="true" className="size-6" />}
        />
      ) : (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {products.map((product) => {
            const tone = coverTones[product.coverTone] ?? coverTones.indigo!;
            return (
              <li key={product.id}>
                <Card className="flex h-full flex-col">
                  <CardContent className="flex flex-1 flex-col gap-3 p-4">
                    <div
                      className="flex h-28 items-center justify-center rounded-lg font-serif text-lg"
                      style={{ backgroundColor: tone.background, color: tone.foreground }}
                    >
                      {product.type === "book" ? <BookOpen className="size-9" aria-hidden="true" /> : "Edumart"}
                    </div>
                    <div className="flex-1">
                      <p className="font-semibold">{product.title}</p>
                      <p className="text-sm text-muted-foreground">{product.subtitle}</p>
                      <p className="mt-1 text-xs text-muted-foreground">
                        {product.publisher} · {product.pages > 0 ? `${product.pages} pages` : "digital"}
                      </p>
                    </div>
                    <div className="flex items-center justify-between gap-2">
                      <p className="font-semibold">{formatMoney(product.priceMinor, product.currency)}</p>
                      {product.owned ? (
                        <Badge variant="success">Owned</Badge>
                      ) : (
                        <BuyButton productId={product.id} />
                      )}
                    </div>
                  </CardContent>
                </Card>
              </li>
            );
          })}
        </ul>
      )}

      <p className="text-xs text-muted-foreground">
        Already bought something?{" "}
        <Link href="/library" className="font-semibold text-primary underline-offset-4 hover:underline">
          Open your library
        </Link>
        .
      </p>
    </div>
  );
}
