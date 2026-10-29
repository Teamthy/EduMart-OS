const CACHE_NAME = "edumart-static-v1";
self.addEventListener("install", (event) => { event.waitUntil(self.skipWaiting()); });
self.addEventListener("activate", (event) => { event.waitUntil((async () => { const keys = await caches.keys(); await Promise.all(keys.filter((key) => key.startsWith("edumart-") && key !== CACHE_NAME).map((key) => caches.delete(key))); await self.clients.claim(); })()); });
self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  // Never cache authentication, signup, API, or personalized navigation responses.
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/login") || url.pathname.startsWith("/signup")) return;
  if (url.pathname.startsWith("/_next/static/") || url.pathname.match(/\.(?:css|js|svg|png|jpg|webp|woff2)$/)) {
    event.respondWith((async () => { const cache = await caches.open(CACHE_NAME); const cached = await cache.match(request); if (cached) return cached; const response = await fetch(request); if (response.ok && response.type === "basic") cache.put(request, response.clone()); return response; })());
  }
});
