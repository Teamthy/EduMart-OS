import { DatabaseSync } from "node:sqlite";
import { readFileSync, mkdirSync } from "node:fs";
import path from "node:path";
import { randomUUID } from "node:crypto";
import { seedDatabase } from "./seed";

/**
 * Single database access layer.
 *
 * Local/CI uses Node's built-in SQLite with `db/schema.sql`. The schema is written to be
 * Postgres-portable (opaque ids, ISO-8601 UTC text timestamps, integer minor units), so moving
 * the system of record to managed PostgreSQL only changes this adapter, not the domain code.
 */

type Row = Record<string, any>;

const globalForDb = globalThis as unknown as {
  __edumartDb?: DatabaseSync;
  __edumartSeeded?: boolean;
};

function dbFile(): string {
  const url = process.env.DATABASE_URL ?? "file:./db/dev.db";
  const relative = url.replace(/^file:/, "");
  return path.isAbsolute(relative) ? relative : path.join(process.cwd(), relative);
}

export function nowIso(): string {
  return new Date().toISOString();
}

export function isoPlus(ms: number): string {
  return new Date(Date.now() + ms).toISOString();
}

export function id(): string {
  return randomUUID();
}

function open(): DatabaseSync {
  const file = dbFile();
  mkdirSync(path.dirname(file), { recursive: true });
  const db = new DatabaseSync(file);
  db.exec("PRAGMA journal_mode = WAL;");
  db.exec("PRAGMA foreign_keys = ON;");
  db.exec("PRAGMA busy_timeout = 5000;");
  const schema = readFileSync(path.join(process.cwd(), "db", "schema.sql"), "utf8");
  db.exec(schema);
  return db;
}

export function getDb(): DatabaseSync {
  if (!globalForDb.__edumartDb) {
    globalForDb.__edumartDb = open();
  }
  const db = globalForDb.__edumartDb;

  // Demo bootstrap. A seed marker makes this deterministic: a partially-applied seed is
  // detected and re-run rather than mistaken for a complete data set.
  const SEED_VERSION = "2026-10-content-v1";
  if (!globalForDb.__edumartSeeded) {
    const marker = db.prepare("SELECT value FROM schema_meta WHERE key = 'seed_version'").get() as Row | undefined;
    if (!marker || marker.value !== SEED_VERSION) {
      db.exec("BEGIN IMMEDIATE");
      try {
        const recheck = db.prepare("SELECT value FROM schema_meta WHERE key = 'seed_version'").get() as Row | undefined;
        if (!recheck || recheck.value !== SEED_VERSION) {
          seedDatabase(db);
          db.prepare(
            "INSERT INTO schema_meta (key, value) VALUES ('seed_version', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
          ).run(SEED_VERSION);
        }
        db.exec("COMMIT");
      } catch (err) {
        try {
          db.exec("ROLLBACK");
        } catch {
          /* ignore */
        }
        throw err;
      }
    }
    globalForDb.__edumartSeeded = true;
  }
  return db;
}

function plain<T>(value: T): T {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return { ...(value as object) } as T;
  }
  return value;
}

export const q = {
  all<T = Row>(sql: string, ...params: unknown[]): T[] {
    return (getDb().prepare(sql).all(...(params as any[])) as any[]).map((r) => plain(r)) as T[];
  },
  get<T = Row>(sql: string, ...params: unknown[]): T | null {
    const row = getDb().prepare(sql).get(...(params as any[]));
    return row ? (plain(row) as T) : null;
  },
  run(sql: string, ...params: unknown[]) {
    return getDb().prepare(sql).run(...(params as any[]));
  },
  exec(sql: string) {
    getDb().exec(sql);
  },
};

/** Run a set of writes atomically. Used for multi-record business operations (submit, order, ledger). */
export function transaction<T>(fn: () => T): T {
  const db = getDb();
  db.exec("BEGIN IMMEDIATE");
  try {
    const result = fn();
    db.exec("COMMIT");
    return result;
  } catch (err) {
    try {
      db.exec("ROLLBACK");
    } catch {
      /* ignore */
    }
    throw err;
  }
}

export function count(table: string): number {
  const row = q.get<{ n: number }>(`SELECT COUNT(*) AS n FROM ${table}`);
  return row ? Number(row.n) : 0;
}
