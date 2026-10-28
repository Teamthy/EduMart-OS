import { scryptSync, randomBytes, timingSafeEqual } from "node:crypto";

/**
 * Password hashing is isolated from the HTTP/session layer so scripts and tests can use it
 * without pulling in Next.js request APIs.
 *
 * scrypt (N=16384, r=8, p=1) with a 16-byte random salt per password. Parameters are stored in
 * the digest so they can be raised later without invalidating existing hashes.
 */
export function hashPassword(password: string): string {
  const salt = randomBytes(16);
  const N = 16384;
  const r = 8;
  const p = 1;
  const hash = scryptSync(password.normalize("NFKC"), salt, 64, { N, r, p, maxmem: 64 * 1024 * 1024 });
  return `scrypt$${N}$${r}$${p}$${salt.toString("base64url")}$${hash.toString("base64url")}`;
}

export function verifyPassword(password: string, stored: string): boolean {
  try {
    const [scheme, N, r, p, saltB64, hashB64] = stored.split("$");
    if (scheme !== "scrypt") return false;
    const salt = Buffer.from(saltB64, "base64url");
    const expected = Buffer.from(hashB64, "base64url");
    const actual = scryptSync(password.normalize("NFKC"), salt, expected.length, {
      N: Number(N),
      r: Number(r),
      p: Number(p),
      maxmem: 64 * 1024 * 1024,
    });
    return timingSafeEqual(expected, actual);
  } catch {
    return false;
  }
}
