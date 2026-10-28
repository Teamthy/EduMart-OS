import type { Metadata } from "next";
import Link from "next/link";
import { BookOpen, Lock } from "lucide-react";

import { coverTones, formatMoney, type Product } from "@edumart/shared";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Alert } from "@/components/ui/alert";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";

export const metadata: Metadata = { title: "Library" };
export const dynamic = "force-dynamic";

export default async function LibraryPage() {
  await requireUser("/library");

  let owned: Product[] = [];
  let catalogue: Product[] = [];
  try {
    const [library, products] = await Promise.all([
      serverFetch<{ products: Product[] }>("/v1/library"),
      serverFetch<{ products: Product[] }>("/v1/products"),
    ]);
    owned = library.products;
    catalogue = products.products.filter((product) => !product.owned);
  } catch (error) {
    return (
      <Alert tone="error" title="We couldn't load your library">
        {error instanceof ServerApiError ? error.message : "Please try again."}
      </Alert>
    );
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Library</h1>
        <p className="text-sm text-muted-foreground">Everything you own, plus revision packs you can add.</p>
      </header>

      <section className="space-y-3">
        <h2 className="font-serif text-lg">Your books</h2>
        {owned.length === 0 ? (
          <EmptyState
            title="Your library is empty"
            description="Purchase a revision pack and it appears here instantly — access is granted by the payment webhook, not by this screen."
            icon={<BookOpen aria-hidden="true" className="size-6" />}
            action={
              <Button asChild variant="outline">
                <Link href="/store">Browse the store</Link>
              </Button>
            }
          />
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {owned.map((product) => (
              <Card key={product.id}>
                <CardContent className="flex items-center justify-between gap-3 p-4">
                  <div className="min-w-0">
                    <p className="truncate font-semibold">{product.title}</p>
                    <p className="truncate text-xs text-muted-foreground">{product.publisher}</p>
                  </div>
                  <Badge variant="success">Owned</Badge>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </section>

      <section className="space-y-3">
        <h2 className="font-serif text-lg">Add to your library</h2>
        {catalogue.length === 0 ? (
          <p className="text-sm text-muted-foreground">You own everything currently available. New packs arrive each term.</p>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {catalogue.map((product) => {
              const tone = coverTones[product.coverTone] ?? coverTones.indigo!;
              return (
                <Card key={product.id}>
                  <CardContent className="space-y-3 p-4">
                    <div
                      className="flex h-24 items-center justify-center rounded-lg font-serif text-lg"
                      style={{ backgroundColor: tone.background, color: tone.foreground }}
                      aria-hidden="true"
                    >
                      {product.type === "book" ? <BookOpen className="size-8" /> : "Edumart"}
                    </div>
                    <div>
                      <p className="font-semibold">{product.title}</p>
                      <p className="text-sm text-muted-foreground">{product.subtitle}</p>
                    </div>
                    <div className="flex items-center justify-between gap-2">
                      <p className="font-semibold">{formatMoney(product.priceMinor, product.currency)}</p>
                      <Button asChild size="sm">
                        <Link href={`/checkout/new?productId=${product.id}`}>
                          <Lock aria-hidden="true" /> Buy
                        </Link>
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}
