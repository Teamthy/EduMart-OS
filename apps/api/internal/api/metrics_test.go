package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/api"
)

// scrape returns the status and body of /metrics, over the loopback connection the harness uses.
func (h *harness) scrape(t *testing.T) (int, string) {
	t.Helper()
	res, err := http.Get(h.server.URL + "/metrics")
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer res.Body.Close()
	return res.StatusCode, readAll(t, res)
}

// createOrder places one order and returns its id, so a test can make an identifier that must not appear in
// a metric label.
func (h *harness) createOrder(token string) string {
	h.t.Helper()
	status, body := h.do("POST", "/v1/orders", token, map[string]any{"productId": h.productID()})
	if status != http.StatusCreated {
		h.t.Fatalf("create order: %d %v", status, body)
	}
	return body["order"].(map[string]any)["id"].(string)
}

// TestMetricsRecordRequestsByRoutePattern is the property that keeps a monitoring system alive: the route
// label is the pattern, so a thousand orders produce one series rather than a thousand.
func TestMetricsRecordRequestsByRoutePattern(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")

	for i := 0; i < 3; i++ {
		if status, body := h.do("GET", "/v1/products", learner, nil); status != http.StatusOK {
			t.Fatalf("products: %d %v", status, body)
		}
	}
	// Two different ids through the same pattern.
	first := h.createOrder(learner)
	if status, body := h.do("GET", "/v1/orders/"+first, learner, nil); status != http.StatusOK {
		t.Fatalf("order: %d %v", status, body)
	}

	status, body := h.scrape(t)
	if status != http.StatusOK {
		t.Fatalf("scrape: %d %s", status, body)
	}
	if !strings.Contains(body, `edumart_http_requests_total{method="GET",route="/v1/products",status="200"} 3`) {
		t.Fatalf("the request counter did not record three calls:\n%s", grepLines(body, "edumart_http_requests_total"))
	}
	if !strings.Contains(body, `route="/v1/orders/{id}"`) {
		t.Fatalf("the route label is not the pattern:\n%s", grepLines(body, "edumart_http_requests_total"))
	}
	// The identifier must never become a label, in any metric.
	if strings.Contains(body, first) {
		t.Fatal("an order id leaked into the metrics exposition")
	}
}

// TestMetricsNeverCarryIdentifiers: a scrape is read by a system outside the API's access controls, so a
// label with an email or a session token in it is a data leak, not a cardinality problem.
func TestMetricsNeverCarryIdentifiers(t *testing.T) {
	h := newHarness(t)
	token := h.login("student@edumart.ng", "Student123!")
	h.createOrder(token)

	_, body := h.scrape(t)
	for _, forbidden := range []string{"student@edumart.ng", "Student123!", token, "AMARA-2026"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("the metrics exposition contains %q", forbidden)
		}
	}
}

// TestErrorsAreCountedByCode covers the alert on INTERNAL_ERROR: a refusal anywhere — handler, limiter or
// router — has to reach the same counter, or the alert sees only the refusals someone remembered to count.
func TestErrorsAreCountedByCode(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")

	if status, _ := h.do("GET", "/v1/admin/metrics", learner, nil); status != http.StatusForbidden {
		t.Fatalf("expected a 403, got %d", status)
	}
	if status, _ := h.do("GET", "/v1/nothing-here", "", nil); status != http.StatusNotFound {
		t.Fatalf("expected a 404, got %d", status)
	}

	_, body := h.scrape(t)
	if !strings.Contains(body, `edumart_api_errors_total{code="FORBIDDEN"} 1`) {
		t.Fatalf("a handler refusal was not counted:\n%s", grepLines(body, "edumart_api_errors_total"))
	}
	if !strings.Contains(body, `edumart_api_errors_total{code="NOT_FOUND"} 1`) {
		t.Fatalf("a router 404 was not counted:\n%s", grepLines(body, "edumart_api_errors_total"))
	}
}

// TestWebhookOutcomesAreVisible keeps the difference between "the provider retried" and "someone is posting
// forged callbacks" out of the anonymous voice of a single error count.
func TestWebhookOutcomesAreVisible(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	orderID := h.createOrder(learner)

	// A forged callback: signed with the wrong secret.
	forged, err := http.NewRequest("POST", h.server.URL+"/v1/webhooks/payments/sandbox", strings.NewReader(`{"id":"evt_1"}`))
	if err != nil {
		t.Fatalf("build forged request: %v", err)
	}
	forged.Header.Set("Content-Type", "application/json")
	forged.Header.Set("x-edumart-signature", "not-the-secret")
	res, err := http.DefaultClient.Do(forged)
	if err != nil {
		t.Fatalf("forged webhook: %v", err)
	}
	res.Body.Close()

	if status, body := h.do("POST", "/v1/orders/"+orderID+"/simulate-payment", learner, nil); status != http.StatusOK {
		t.Fatalf("simulate payment: %d %v", status, body)
	}

	_, metricsBody := h.scrape(t)
	if !strings.Contains(metricsBody, `edumart_webhook_events_total{outcome="rejected"} 1`) {
		t.Fatalf("a forged webhook was not counted as rejected:\n%s", grepLines(metricsBody, "edumart_webhook"))
	}
	if !strings.Contains(metricsBody, `edumart_webhook_events_total{outcome="accepted"} 1`) {
		t.Fatalf("a settled payment was not counted as accepted:\n%s", grepLines(metricsBody, "edumart_webhook"))
	}
}

// TestReconciliationPublishesItsDisagreements: the report exists to be pulled, but an alert has to be able
// to see it without anyone opening it. The gauge is set, not incremented.
func TestReconciliationPublishesItsDisagreements(t *testing.T) {
	h := newHarness(t)
	learner := h.login("student@edumart.ng", "Student123!")
	orderID := h.paidOrder(learner)["id"].(string)
	finance := h.financeToken()

	// A clean run first: the gauge must be zero, not absent.
	if status, body := h.do("GET", "/v1/admin/finance/reconciliation", finance, nil); status != http.StatusOK {
		t.Fatalf("reconciliation: %d %v", status, body)
	}
	_, body := h.scrape(t)
	if !strings.Contains(body, `edumart_reconciliation_discrepancies{kind="paid_without_entitlement"} 0`) {
		t.Fatalf("a clean reconciliation should report zero, not nothing:\n%s",
			grepLines(body, "edumart_reconciliation"))
	}

	// Break one invariant behind the service's back, then re-run the report.
	if _, err := h.store.DB.Exec(`UPDATE entitlements SET status = 'revoked' WHERE order_id = ?`, orderID); err != nil {
		t.Fatalf("break the entitlement: %v", err)
	}
	if status, body := h.do("GET", "/v1/admin/finance/reconciliation", finance, nil); status != http.StatusOK {
		t.Fatalf("reconciliation after breaking: %d %v", status, body)
	}
	_, body = h.scrape(t)
	if !strings.Contains(body, `edumart_reconciliation_discrepancies{kind="paid_without_entitlement"} 1`) {
		t.Fatalf("the discrepancy did not reach the gauge:\n%s", grepLines(body, "edumart_reconciliation"))
	}
}

// TestMetricsAreNotPublic is the access policy. Without a token only a loopback caller may scrape, and the
// refusal is the same 404 an unknown path gets: an unauthenticated prober should not learn the endpoint is
// there.
func TestMetricsAreNotPublic(t *testing.T) {
	h := newHarness(t)

	// The test harness connects over loopback, so simulate an external caller by asking the policy directly
	// through a request with a routable remote address.
	req := httptest.NewRequest("GET", "/metrics", nil)
	req.RemoteAddr = "203.0.113.9:34567"
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an external caller got %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "edumart_http_requests_total") {
		t.Fatal("an external caller received the exposition")
	}
}

// TestATokenGuardsMetricsWhenConfigured: with a token set, the scrape must present it — and a wrong token
// must be refused, so a deployment cannot accidentally publish its metrics.
func TestATokenGuardsMetricsWhenConfigured(t *testing.T) {
	h := newHarnessWithConfig(t, func(c *api.Config) { c.MetricsToken = "scrape-me" })

	req := httptest.NewRequest("GET", "/metrics", nil)
	req.RemoteAddr = "127.0.0.1:5000" // loopback is not enough once a token exists
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a scrape with no token got %d, want 404", rec.Code)
	}

	wrong := httptest.NewRequest("GET", "/metrics", nil)
	wrong.RemoteAddr = "127.0.0.1:5000"
	wrong.Header.Set("Authorization", "Bearer wrong-token")
	rec = httptest.NewRecorder()
	h.router.ServeHTTP(rec, wrong)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a scrape with the wrong token got %d, want 404", rec.Code)
	}

	right := httptest.NewRequest("GET", "/metrics", nil)
	right.RemoteAddr = "127.0.0.1:5000"
	right.Header.Set("Authorization", "Bearer scrape-me")
	rec = httptest.NewRecorder()
	h.router.ServeHTTP(rec, right)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "edumart_http_requests_total") {
		t.Fatalf("the configured token was refused: %d %s", rec.Code, rec.Body.String()[:200])
	}
}

// TestBackupHeartbeatReachesTheExposition closes the loop with the backup work: the file the backup job
// writes is what the freshness alert reads, so a stale one has to be visible here.
func TestBackupHeartbeatReachesTheExposition(t *testing.T) {
	heartbeat := t.TempDir() + "/backup-heartbeat.json"
	writeText(t, heartbeat, `{"at":"2026-10-29T02:17:00Z","engine":"postgres","file":"x.dump","rows":201,"sha256":"abc","verifiedAt":true}`)

	h := newHarnessWithConfig(t, func(c *api.Config) { c.BackupHeartbeat = heartbeat })
	if status, body := h.scrape(t); status != http.StatusOK {
		t.Fatalf("scrape: %d %s", status, body)
	}
	_, body := h.scrape(t)
	if !strings.Contains(body, "edumart_backup_rows 201") {
		t.Fatalf("the backup heartbeat did not reach the exposition:\n%s", grepLines(body, "edumart_backup"))
	}
	if !strings.Contains(body, "edumart_backup_last_run_succeeded 1") {
		t.Fatalf("a verified backup should report success:\n%s", grepLines(body, "edumart_backup"))
	}
	if !strings.Contains(body, "edumart_backup_age_seconds ") {
		t.Fatalf("the backup age is missing, which is the metric the alert uses:\n%s", grepLines(body, "edumart_backup"))
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func readAll(t *testing.T, res *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(raw)
}

// grepLines keeps a failure message to the metrics that matter, rather than the whole exposition.
func grepLines(body, prefix string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func writeText(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestEveryMetricAnAlertRuleNamesExists closes the one hole in alerting that nothing else catches: a rule
// pointing at a metric that was renamed. Prometheus accepts the rule, the expression simply never matches,
// and the alert is silently never connected to anything — which on a dashboard is indistinguishable from a
// system with nothing wrong.
//
// The exposition lists every registered family in its TYPE lines even when it has no samples yet, so this
// checks against the whole surface rather than only what a test happened to exercise.
// scripts/check-alerts.py runs the same check in CI against a live scrape; this one runs in `go test`, where
// it is cheap enough to be run before every commit.
func TestEveryMetricAnAlertRuleNamesExists(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "infra", "observability", "alerts.yml"))
	if err != nil {
		t.Fatalf("read the alert rules: %v", err)
	}

	h := newHarness(t)
	status, body := h.scrape(t)
	if status != http.StatusOK {
		t.Fatalf("scrape: %d", status)
	}

	known := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# TYPE ") {
			known[strings.Fields(line)[2]] = true
		}
	}
	if len(known) == 0 {
		t.Fatal("the exposition declares no metric families, so this test would pass vacuously")
	}

	// Names the API does not own: the monitoring system's own liveness, and the blackbox probe.
	external := map[string]bool{"up": true, "probe_success": true}

	referenced := regexp.MustCompile(`\bedumart_[a-z0-9_]+`).FindAllString(string(spec), -1)
	if len(referenced) < 5 {
		t.Fatalf("only %d metric names found in the rules; the extraction is probably broken", len(referenced))
	}
	for _, name := range referenced {
		if external[name] {
			continue
		}
		// A histogram rule names `_bucket`; the family is declared under the bare name.
		base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, "_bucket"), "_sum"), "_count")
		if !known[base] && !known[name] {
			t.Errorf("alert rules reference %q, which the API never emits: that alert can never fire", name)
		}
	}
}
