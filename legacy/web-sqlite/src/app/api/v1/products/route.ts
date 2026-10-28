import { handle, ok } from "@/lib/server/http";
import { listProducts } from "@/lib/server/commerce";

export async function GET(req: Request) {
  return handle(req, async () => {
    const url = new URL(req.url);
    const products = listProducts({
      q: url.searchParams.get("q") ?? undefined,
      subject: url.searchParams.get("subject") ?? undefined,
      exam: url.searchParams.get("exam") ?? undefined,
      type: url.searchParams.get("type") ?? undefined,
    });
    return ok({ products });
  });
}
