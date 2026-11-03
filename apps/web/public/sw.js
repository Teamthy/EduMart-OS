/**
 * Edumart service worker.
 *
 * It caches the app shell and static assets only. API responses (`/v1/*`) are never cached: an
 * attempt's status, deadline and score must always come from the server, and a stale cached answer
 * state would let the UI lie about what has been saved.
 */
const SHELL = "edumart-shell-v1";
const OFFLINE_URL = "/offline";
const PRECACHE = ["/", OFFLINE_URL, "/manifest.webmanifest", "/icon.svg"];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(SHELL)
      .then((cache) => cache.addAll(PRECACHE))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((key) => key !== SHELL).map((key) => caches.delete(key))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  // Never cache API traffic or authenticated documents.
  if (url.pathname.startsWith("/v1/") || url.pathname.startsWith("/api/")) return;
  // Nor the media encryption worker. It carries a frame format version, and a cached worker from an earlier deploy
  // would seal frames the current app cannot open — the failure looks like "the video is broken", which is the
  // hardest kind of bug to attribute. It is a few kilobytes; fetching it is cheaper than that.
  if (url.pathname === "/media-e2ee-worker.js") return;

  if (request.mode === "navigate") {
    event.respondWith(
      fetch(request).catch(async () => {
        const cache = await caches.open(SHELL);
        return (await cache.match(OFFLINE_URL)) ?? new Response("Offline", { status: 503 });
      }),
    );
    return;
  }

  if (url.pathname.startsWith("/_next/static/") || /\.(?:css|js|svg|png|woff2?)$/.test(url.pathname)) {
    event.respondWith(
      caches.match(request).then(
        (cached) =>
          cached ??
          fetch(request).then((response) => {
            const copy = response.clone();
            caches.open(SHELL).then((cache) => cache.put(request, copy));
            return response;
          }),
      ),
    );
  }
});
