package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/metrics"
)

// metricsHandler serves the scrape endpoint.
//
// Metrics are not a public API. They describe load, failures and money, and a monitoring system is not
// covered by the API's own authentication — so the endpoint has an explicit policy rather than an
// assumption, and it *fails closed*:
//
//   - A token is configured (the production case): the scrape must present it as a bearer token. A
//     comparison in constant time, because a token check that leaks its length prefix is a token check
//     that can be attacked from the outside.
//   - No token is configured (local development): only a loopback caller may scrape. That keeps
//     `curl localhost:8080/metrics` working and keeps the endpoint off the public internet by default,
//     which is the property that matters — the alternative, "open because it is convenient", is how
//     internal metrics end up indexed by a search engine.
//
// Either way the response carries no identifiers: no user id, no email, no order id. A metric label is
// visible to whoever can scrape, and that set of people is not the set of people allowed to read orders.
func metricsHandler(platform *metrics.Platform, token, heartbeatPath string) http.HandlerFunc {
	registry := platform.Registry()
	return func(w http.ResponseWriter, r *http.Request) {
		if !mayScrape(r, token) {
			// 404 rather than 403: an unauthenticated prober should not learn that a metrics endpoint
			// exists here at all. The distinction costs nothing and removes a fingerprint.
			httpx.JSON(w, http.StatusNotFound, map[string]any{
				"error": map[string]any{
					"code":       httpx.CodeNotFound,
					"message":    "That endpoint does not exist.",
					"request_id": httpx.RequestID(r.Context()),
					"details":    []any{},
				},
			})
			return
		}
		// Re-read the heartbeat on every scrape. Reading it once at boot would report the age of the
		// backup as of the last restart, which is exactly the number that looks fine forever.
		platform.ReadBackupHeartbeat(heartbeatPath)
		registry.ServeHTTP(w, r)
	}
}

func mayScrape(r *http.Request, token string) bool {
	if token != "" {
		presented := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		return subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1
	}
	return isLoopback(r.RemoteAddr)
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if zone := strings.LastIndex(host, "%"); zone >= 0 { // fe80::1%eth0
		host = host[:zone]
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
