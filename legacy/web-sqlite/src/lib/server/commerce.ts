import { createHmac, randomUUID, timingSafeEqual, createHash } from "node:crypto";
import { q, id, nowIso, transaction } from "./db";
import { ApiError } from "./http";
import { audit, enqueue, track } from "./audit";

/**
 * Commerce: server-calculated totals, verified provider state, idempotent entitlement grants.
 * A browser redirect is never proof of payment — only a signature-verified webhook is.
 */

const WEBHOOK_SECRET = process.env.PAYMENT_WEBHOOK_SECRET ?? "dev_sandbox_webhook_secret";

export function listProducts(opts: { q?: string; subject?: string; exam?: string; type?: string } = {}) {
  const where = ["p.status = 'published'"];
  const params: unknown[] = [];
  if (opts.q) {
    where.push("(p.title LIKE ? OR p.subtitle LIKE ? OR p.description LIKE ?)");
    params.push(`%${opts.q}%`, `%${opts.q}%`, `%${opts.q}%`);
  }
  if (opts.subject) {
    where.push("p.subject_key = ?");
    params.push(opts.subject);
  }
  if (opts.exam) {
    where.push("p.exam_key = ?");
    params.push(opts.exam);
  }
  if (opts.type) {
    where.push("p.type = ?");
    params.push(opts.type);
  }
  return q.all<any>(
    `SELECT p.*, pub.name AS publisher_name,
            (SELECT COUNT(*) FROM entitlements e WHERE e.product_id = p.id AND e.status = 'active') AS holders
       FROM products p LEFT JOIN publishers pub ON pub.id = p.publisher_id
      WHERE ${where.join(" AND ")} ORDER BY p.price_minor ASC`,
    ...params,
  );
}

export function productBySlug(slug: string) {
  return q.get<any>(
    `SELECT p.*, pub.name AS publisher_name, pub.status AS publisher_status
       FROM products p LEFT JOIN publishers pub ON pub.id = p.publisher_id
      WHERE p.slug = ?`,
    slug,
  );
}

export function entitlementFor(userId: string, productId: string) {
  return q.get<any>(
    `SELECT * FROM entitlements WHERE user_id = ? AND product_id = ? AND status = 'active'
       AND (expires_at IS NULL OR expires_at > ?)`,
    userId,
    productId,
    nowIso(),
  );
}

export function listEntitlements(userId: string) {
  return q.all<any>(
    `SELECT e.id, e.status, e.starts_at, e.expires_at, p.slug, p.title, p.subtitle, p.cover_tone, p.type, p.pages
       FROM entitlements e JOIN products p ON p.id = e.product_id
      WHERE e.user_id = ? AND e.status = 'active' ORDER BY e.starts_at DESC`,
    userId,
  );
}

/** Totals always come from active price records, never from a client-supplied amount. */
export function createOrder(userId: string, productSlugs: string[]) {
  if (productSlugs.length === 0) throw new ApiError("VALIDATION_ERROR", "Add at least one item to your order.");

  const products = productSlugs.map((slug) => {
    const product = productBySlug(slug);
    if (!product || product.status !== "published") throw new ApiError("NOT_FOUND", "One of the items is no longer available.");
    return product;
  });

  const existing = products.find((p) => entitlementFor(userId, p.id));
  if (existing) throw new ApiError("CONFLICT", `You already have access to ${existing.title}.`);

  const subtotal = products.reduce((sum, p) => sum + Number(p.price_minor), 0);
  const orderId = id();
  const reference = `EDM-${randomUUID().replace(/-/g, "").slice(0, 10).toUpperCase()}`;

  transaction(() => {
    q.run(
      `INSERT INTO orders (id, user_id, status, currency, subtotal_minor, discount_minor, total_minor, reference, created_at, paid_at)
       VALUES (?,?,'pending_payment','NGN',?,0,?,?,?,NULL)`,
      orderId,
      userId,
      subtotal,
      subtotal,
      reference,
      nowIso(),
    );
    for (const product of products) {
      q.run(
        `INSERT INTO order_items (id, order_id, product_id, title_snapshot, unit_amount_minor, quantity)
         VALUES (?,?,?,?,?,1)`,
        id(),
        orderId,
        product.id,
        product.title,
        Number(product.price_minor),
      );
    }
  });

  track({ userId, eventType: "order_created", entityType: "order", entityId: orderId, metadata: { subtotal } });
  return getOrder(userId, orderId)!;
}

export function getOrder(userId: string, orderId: string) {
  const order = q.get<any>("SELECT * FROM orders WHERE id = ?", orderId);
  if (!order) return null;
  if (order.user_id !== userId) return null; // no cross-user leakage
  const items = q.all<any>(
    `SELECT oi.*, p.slug, p.cover_tone FROM order_items oi JOIN products p ON p.id = oi.product_id WHERE oi.order_id = ?`,
    orderId,
  );
  const payment = q.get<any>("SELECT * FROM payment_attempts WHERE order_id = ?", orderId);
  return { ...order, items, payment };
}

export function listOrders(userId: string) {
  return q.all<any>(
    `SELECT o.*, (SELECT COUNT(*) FROM order_items oi WHERE oi.order_id = o.id) AS item_count
       FROM orders o WHERE o.user_id = ? ORDER BY o.created_at DESC LIMIT 20`,
    userId,
  );
}

/**
 * Creates a provider payment intent. In the sandbox this returns a checkout token that the
 * "provider" page confirms; in production this is a real provider call with an idempotency key.
 */
export function initiatePayment(userId: string, orderId: string) {
  const order = getOrder(userId, orderId);
  if (!order) throw new ApiError("NOT_FOUND", "That order could not be found.");
  if (order.status === "paid" || order.status === "fulfilled") {
    throw new ApiError("CONFLICT", "This order is already paid.");
  }
  if (order.total_minor === 0) {
    // Free items still go through an explicit, audited grant — not an implicit entitlement.
    grantEntitlements(order.id, userId);
    q.run("UPDATE orders SET status = 'paid', paid_at = ? WHERE id = ?", nowIso(), order.id);
    q.run("UPDATE orders SET status = 'fulfilled' WHERE id = ?", order.id);
    return { status: "paid" as const, order: getOrder(userId, orderId)! };
  }

  const existing = q.get<any>("SELECT * FROM payment_attempts WHERE order_id = ?", orderId);
  if (existing && existing.status === "pending") {
    return {
      status: "pending" as const,
      provider_reference: existing.provider_reference,
      amount_minor: existing.amount_minor,
      currency: existing.currency,
      checkout_url: `/checkout/pay/${existing.provider_reference}`,
    };
  }

  const reference = `sbx_${randomUUID().replace(/-/g, "").slice(0, 16)}`;
  q.run(
    `INSERT INTO payment_attempts (id, order_id, provider, provider_reference, status, amount_minor, currency, idempotency_key, created_at)
     VALUES (?,?,'sandbox',?,'pending',?,?,?,?)`,
    id(),
    orderId,
    reference,
    order.total_minor,
    order.currency,
    `pay:${orderId}`,
    nowIso(),
  );
  audit({
    actorUserId: userId,
    action: "payment.initiated",
    resourceType: "order",
    resourceId: orderId,
    metadata: { reference, amountMinor: order.total_minor },
  });
  return {
    status: "pending" as const,
    provider_reference: reference,
    amount_minor: order.total_minor,
    currency: order.currency,
    checkout_url: `/checkout/pay/${reference}`,
  };
}

export function signPayload(rawBody: string): string {
  return createHmac("sha256", WEBHOOK_SECRET).update(rawBody).digest("hex");
}

export function verifySignature(rawBody: string, signature: string | null): boolean {
  if (!signature) return false;
  const expected = signPayload(rawBody);
  const a = Buffer.from(expected);
  const b = Buffer.from(signature);
  return a.length === b.length && timingSafeEqual(a, b);
}

/**
 * Webhook processing: signature → dedupe on (provider,event_id) → validate amount/currency →
 * transactional state change → idempotent entitlement grant.
 */
export function processProviderEvent(provider: string, rawBody: string, signature: string | null) {
  const valid = verifySignature(rawBody, signature);
  let payload: any = {};
  try {
    payload = JSON.parse(rawBody);
  } catch {
    throw new ApiError("VALIDATION_ERROR", "Malformed webhook payload.");
  }
  const eventId = String(payload.event_id ?? "");
  if (!eventId) throw new ApiError("VALIDATION_ERROR", "Missing event id.");

  const payloadHash = createHash("sha256").update(rawBody).digest("hex");
  const existing = q.get<any>("SELECT * FROM provider_events WHERE provider = ? AND event_id = ?", provider, eventId);
  if (existing) {
    return { received: true, duplicate: true, processed: existing.processing_status === "processed" };
  }

  q.run(
    `INSERT INTO provider_events (id, provider, event_id, signature_valid, processing_status, payload_hash, received_at, processed_at)
     VALUES (?,?,?,?,'received',?,?,NULL)`,
    id(),
    provider,
    eventId,
    valid ? 1 : 0,
    payloadHash,
    nowIso(),
  );

  if (!valid) {
    q.run(
      "UPDATE provider_events SET processing_status = 'failed' WHERE provider = ? AND event_id = ?",
      provider,
      eventId,
    );
    throw new ApiError("FORBIDDEN", "Invalid webhook signature.");
  }

  const reference = String(payload.provider_reference ?? "");
  const attempt = q.get<any>("SELECT * FROM payment_attempts WHERE provider_reference = ?", reference);
  if (!attempt) {
    q.run("UPDATE provider_events SET processing_status = 'failed' WHERE provider = ? AND event_id = ?", provider, eventId);
    throw new ApiError("NOT_FOUND", "Unknown payment reference.");
  }

  // Amount and currency are validated against our own record — never trusted from the webhook alone.
  if (Number(payload.amount_minor) !== attempt.amount_minor || payload.currency !== attempt.currency) {
    q.run("UPDATE provider_events SET processing_status = 'failed' WHERE provider = ? AND event_id = ?", provider, eventId);
    q.run("UPDATE payment_attempts SET status = 'failed' WHERE id = ?", attempt.id);
    audit({
      action: "payment.mismatch",
      resourceType: "payment_attempt",
      resourceId: attempt.id,
      reason: "Amount or currency mismatch",
      metadata: { expected: attempt.amount_minor, received: payload.amount_minor },
    });
    throw new ApiError("CONFLICT", "Payment amount does not match the order.");
  }

  transaction(() => {
    q.run("UPDATE provider_events SET processing_status = 'processed', processed_at = ? WHERE provider = ? AND event_id = ?", nowIso(), provider, eventId);

    if (payload.status === "succeeded") {
      q.run("UPDATE payment_attempts SET status = 'succeeded' WHERE id = ? AND status != 'succeeded'", attempt.id);
      const order = q.get<any>("SELECT * FROM orders WHERE id = ?", attempt.order_id);
      if (order && order.status !== "paid" && order.status !== "fulfilled") {
        q.run("UPDATE orders SET status = 'paid', paid_at = ? WHERE id = ? AND status = 'pending_payment'", nowIso(), order.id);
        q.run("UPDATE orders SET status = 'fulfilled' WHERE id = ?", order.id);
        grantEntitlements(order.id, order.user_id);
      }
    } else if (payload.status === "failed") {
      q.run("UPDATE payment_attempts SET status = 'failed' WHERE id = ?", attempt.id);
      q.run("UPDATE orders SET status = 'payment_failed' WHERE id = ? AND status = 'pending_payment'", attempt.order_id);
    }
  });

  const order = q.get<any>("SELECT * FROM orders WHERE id = ?", attempt.order_id);
  if (order) track({ userId: order.user_id, eventType: "payment_confirmed", entityType: "order", entityId: order.id });
  enqueue("payment.reconcile", { orderId: attempt.order_id }, 60_000);

  return { received: true, duplicate: false, processed: true };
}

/** Idempotent entitlement grant: safe to call from a retried webhook. */
export function grantEntitlements(orderId: string, userId: string) {
  const items = q.all<any>("SELECT product_id FROM order_items WHERE order_id = ?", orderId);
  for (const item of items) {
    const existing = q.get<any>("SELECT id FROM entitlements WHERE user_id = ? AND product_id = ?", userId, item.product_id);
    if (existing) {
      q.run("UPDATE entitlements SET status = 'active', revoked_at = NULL WHERE id = ?", existing.id);
      continue;
    }
    q.run(
      `INSERT INTO entitlements (id, user_id, product_id, order_id, status, starts_at, expires_at, revoked_at)
       VALUES (?,?,?,?,'active',?,NULL,NULL)`,
      id(),
      userId,
      item.product_id,
      orderId,
      nowIso(),
    );
  }
}

export function refundOrder(orderId: string, actorUserId: string, reason: string) {
  const order = q.get<any>("SELECT * FROM orders WHERE id = ?", orderId);
  if (!order) throw new ApiError("NOT_FOUND", "Order not found.");
  if (order.status !== "paid" && order.status !== "fulfilled") {
    throw new ApiError("CONFLICT", "Only paid orders can be refunded.");
  }
  transaction(() => {
    q.run("UPDATE orders SET status = 'refunded' WHERE id = ?", orderId);
    q.run("UPDATE entitlements SET status = 'revoked', revoked_at = ? WHERE order_id = ?", nowIso(), orderId);
    q.run("UPDATE payment_attempts SET status = 'failed' WHERE order_id = ?", orderId);
  });
  audit({
    actorUserId,
    action: "order.refunded",
    resourceType: "order",
    resourceId: orderId,
    reason,
    metadata: { amountMinor: order.total_minor },
  });
  return getOrder(order.user_id, orderId);
}
