import { q, id, nowIso, transaction } from "./db";
import { ApiError } from "./http";
import { audit } from "./audit";

/**
 * Two separate assets, two separate append-only ledgers:
 *   • learning points (non-cash, engagement only)
 *   • monetary commissions (currency, eligibility-gated, payouts disabled)
 * Corrections are compensating entries; nothing is ever rewritten in place.
 */

export function pointsSummary(userId: string) {
  const balance = q.get<any>(
    "SELECT COALESCE(SUM(amount), 0) AS balance FROM points_ledger_entries WHERE user_id = ?",
    userId,
  );
  const ledger = q.all<any>(
    `SELECT id, amount, entry_type, source_type, source_id, note, created_at
       FROM points_ledger_entries WHERE user_id = ? ORDER BY created_at DESC LIMIT 20`,
    userId,
  );
  return { balance: Number(balance?.balance ?? 0), ledger };
}

export function commissionSummary(userId: string) {
  const rows = q.all<any>(
    `SELECT status, COALESCE(SUM(amount_minor), 0) AS total, COUNT(*) AS entries
       FROM commission_ledger_entries WHERE user_id = ? GROUP BY status`,
    userId,
  );
  const ledger = q.all<any>(
    `SELECT id, amount_minor, currency, entry_type, source_type, status, created_at
       FROM commission_ledger_entries WHERE user_id = ? ORDER BY created_at DESC LIMIT 20`,
    userId,
  );
  const byStatus = Object.fromEntries(rows.map((r) => [r.status, Number(r.total)]));
  return {
    currency: "NGN",
    pendingMinor: byStatus.pending ?? 0,
    eligibleMinor: byStatus.eligible ?? 0,
    reversedMinor: byStatus.reversed ?? 0,
    settledMinor: byStatus.settled ?? 0,
    payoutsEnabled: false, // gated on legal, accounting, provider and fraud sign-off
    ledger,
  };
}

export function ensureReferralCode(userId: string, displayName: string) {
  const existing = q.get<any>("SELECT * FROM referral_codes WHERE owner_user_id = ?", userId);
  if (existing) return existing;
  const base = displayName
    .toUpperCase()
    .replace(/[^A-Z]/g, "")
    .slice(0, 6) || "EDUMART";
  let code = `${base}-${Math.random().toString(36).slice(2, 6).toUpperCase()}`;
  while (q.get("SELECT id FROM referral_codes WHERE code = ?", code)) {
    code = `${base}-${Math.random().toString(36).slice(2, 6).toUpperCase()}`;
  }
  const rowId = id();
  q.run(
    "INSERT INTO referral_codes (id, owner_user_id, code, status, created_at) VALUES (?,?,?, 'active', ?)",
    rowId,
    userId,
    code,
    nowIso(),
  );
  return q.get<any>("SELECT * FROM referral_codes WHERE id = ?", rowId);
}

/**
 * Attribution is recorded once per referred learner. A referral alone never pays commission —
 * it only creates a pending attribution that must be qualified by an eligible business event.
 */
export function attributeReferral(referredUserId: string, code: string) {
  const referral = q.get<any>("SELECT * FROM referral_codes WHERE code = ? AND status = 'active'", code.toUpperCase());
  if (!referral) return { attributed: false, reason: "unknown_code" as const };
  if (referral.owner_user_id === referredUserId) return { attributed: false, reason: "self_referral" as const };

  const existing = q.get<any>("SELECT id FROM referral_attributions WHERE referred_user_id = ?", referredUserId);
  if (existing) return { attributed: false, reason: "already_attributed" as const };

  q.run(
    "INSERT INTO referral_attributions (id, referred_user_id, referral_code_id, attributed_at, qualifying_event_id, status) VALUES (?,?,?,?,NULL,'pending')",
    id(),
    referredUserId,
    referral.id,
    nowIso(),
  );
  audit({
    actorUserId: referredUserId,
    action: "referral.attributed",
    resourceType: "referral_code",
    resourceId: referral.id,
    metadata: { code: referral.code },
  });
  return { attributed: true as const };
}

/** Qualifies an attribution after a legitimate paid transaction, with caps and idempotency. */
export function qualifyReferral(referredUserId: string, orderId: string, amountMinor: number) {
  const attribution = q.get<any>(
    `SELECT ra.*, rc.owner_user_id FROM referral_attributions ra
       JOIN referral_codes rc ON rc.id = ra.referral_code_id
      WHERE ra.referred_user_id = ? AND ra.status = 'pending'`,
    referredUserId,
  );
  if (!attribution) return null;

  const COMMISSION_RATE = 0.1; // 10% of a qualifying purchase, capped
  const CAP_MINOR = 200_000;
  const commission = Math.min(Math.round(amountMinor * COMMISSION_RATE), CAP_MINOR);
  if (commission <= 0) return null;

  transaction(() => {
    q.run(
      "UPDATE referral_attributions SET status = 'qualified', qualifying_event_id = ? WHERE id = ?",
      orderId,
      attribution.id,
    );
    q.run(
      `INSERT OR IGNORE INTO commission_ledger_entries
        (id, user_id, amount_minor, currency, entry_type, source_type, source_id, status, idempotency_key, created_at)
       VALUES (?,?,?,'NGN','accrue','referral_qualified_order',?, 'pending', ?, ?)`,
      id(),
      attribution.owner_user_id,
      commission,
      orderId,
      `commission:order:${orderId}`,
      nowIso(),
    );
  });
  return { commissionMinor: commission };
}

export function reverseCommission(orderId: string, reason: string, actorUserId?: string) {
  const entry = q.get<any>("SELECT * FROM commission_ledger_entries WHERE source_id = ? AND entry_type = 'accrue'", orderId);
  if (!entry) return null;
  q.run(
    `INSERT OR IGNORE INTO commission_ledger_entries
      (id, user_id, amount_minor, currency, entry_type, source_type, source_id, status, idempotency_key, created_at)
     VALUES (?,?,?,?, 'reverse','refund_reversal',?, 'reversed', ?, ?)`,
    id(),
    entry.user_id,
    -entry.amount_minor,
    entry.currency,
    orderId,
    `commission:reverse:${orderId}`,
    nowIso(),
  );
  audit({
    actorUserId: actorUserId ?? null,
    action: "commission.reversed",
    resourceType: "order",
    resourceId: orderId,
    reason,
  });
  return true;
}

export function adjustPoints(actorUserId: string, userId: string, amount: number, reason: string) {
  if (!Number.isInteger(amount) || amount === 0) throw new ApiError("VALIDATION_ERROR", "Adjustment must be a non-zero integer.");
  if (!reason || reason.trim().length < 5) throw new ApiError("VALIDATION_ERROR", "A reason of at least 5 characters is required.");
  q.run(
    `INSERT INTO points_ledger_entries (id, user_id, amount, entry_type, source_type, source_id, rule_version, idempotency_key, note, created_at)
     VALUES (?,?,?,'adjust','admin',?, 'v1', ?, ?, ?)`,
    id(),
    userId,
    amount,
    actorUserId,
    `points:admin:${id()}`,
    reason,
    nowIso(),
  );
  audit({ actorUserId, action: "points.adjusted", resourceType: "user", resourceId: userId, reason, metadata: { amount } });
}
