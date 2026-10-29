package metrics

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCountersAndGaugesRenderInTheExpositionFormat checks the exact text a scraper will parse. A metric
// that exists but renders wrong is a metric nobody notices is missing.
func TestCountersAndGaugesRenderInTheExpositionFormat(t *testing.T) {
	r := NewRegistry()
	requests := r.Counter("edumart_http_requests_total", "Requests.", "route", "status")
	requests.With("/v1/products", "200").Inc()
	requests.With("/v1/products", "200").Inc()
	requests.With("/v1/orders/{id}", "404").Inc()

	gauge := r.Gauge("edumart_paid_orders", "Paid orders.")
	gauge.Set(7)

	out := r.Render()
	for _, want := range []string{
		"# HELP edumart_http_requests_total Requests.",
		"# TYPE edumart_http_requests_total counter",
		`edumart_http_requests_total{route="/v1/products",status="200"} 2`,
		`edumart_http_requests_total{route="/v1/orders/{id}",status="404"} 1`,
		"# TYPE edumart_paid_orders gauge",
		"edumart_paid_orders 7",
		"# TYPE edumart_process_uptime_seconds gauge",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("exposition is missing %q\n%s", want, out)
		}
	}
}

// TestHistogramsAreCumulative is the property Prometheus relies on: each bucket counts everything at or
// below its bound, and the +Inf bucket equals the count. A non-cumulative histogram renders fine and
// produces nonsense quantiles.
func TestHistogramsAreCumulative(t *testing.T) {
	r := NewRegistry()
	h := r.Histogram("edumart_request_duration_seconds", "Duration.", "route")
	for _, value := range []float64{0.004, 0.02, 0.2, 3} {
		h.With("/v1/products").Observe(value)
	}
	out := r.Render()

	for _, want := range []string{
		`edumart_request_duration_seconds_bucket{route="/v1/products",le="0.005"} 1`,
		`edumart_request_duration_seconds_bucket{route="/v1/products",le="0.025"} 2`,
		`edumart_request_duration_seconds_bucket{route="/v1/products",le="0.25"} 3`,
		`edumart_request_duration_seconds_bucket{route="/v1/products",le="+Inf"} 4`,
		`edumart_request_duration_seconds_count{route="/v1/products"} 4`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("histogram is missing %q\n%s", want, out)
		}
	}
	if !strings.Contains(out, `edumart_request_duration_seconds_sum{route="/v1/products"} 3.224`) {
		t.Fatalf("histogram sum is wrong\n%s", out)
	}
}

// TestLabelValuesAreEscaped: a label containing a quote or a newline would otherwise break the exposition
// for every metric after it, which is a denial of monitoring rather than a cosmetic bug.
func TestLabelValuesAreEscaped(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("edumart_test_total", "Test.", "label")
	c.With("a\"b\\c\nd").Inc()
	out := r.Render()
	if !strings.Contains(out, `edumart_test_total{label="a\"b\\c\nd"} 1`) {
		t.Fatalf("label was not escaped:\n%s", out)
	}
	if strings.Count(out, "\n") < 3 {
		t.Fatalf("an escaped newline broke the exposition into fewer lines than expected:\n%s", out)
	}
}

// TestCountersCannotGoBackwards: a counter that can decrease produces negative rates, and an alert on a
// rate would fire on a restart. Rejecting the decrement is the honest behaviour.
func TestCountersCannotGoBackwards(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("edumart_test_total", "Test.")
	c.Add(5)
	c.Add(-3)
	if !strings.Contains(r.Render(), "edumart_test_total 5") {
		t.Fatalf("a negative increment changed a counter:\n%s", r.Render())
	}
}

// TestConcurrentUpdatesAreSafe: this is on the request path of every route, so a data race here is a
// production crash rather than a flaky test. CI runs the suite with -race.
func TestConcurrentUpdatesAreSafe(t *testing.T) {
	r := NewRegistry()
	platform := NewPlatform(r)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			platform.CountError("CONFLICT")
			platform.Webhook("accepted")
			platform.LoginFailure()
			platform.Refund("NGN", 100)
			platform.ReconciliationReport(nowOrZero(), 3, map[string]int{"paid_without_entitlement": 1})
		}()
	}
	wg.Wait()
	out := r.Render()
	if !strings.Contains(out, `edumart_api_errors_total{code="CONFLICT"} 50`) {
		t.Fatalf("concurrent increments were lost:\n%s", out)
	}
	if !strings.Contains(out, `edumart_webhook_events_total{outcome="accepted"} 50`) {
		t.Fatalf("concurrent webhook counters were lost:\n%s", out)
	}
}

// TestRegisteringTheSameMetricTwiceWithADifferentShapePanics: duplicate families with one name are
// rejected by Prometheus outright, so failing at startup is better than a scrape that never succeeds.
func TestRegisteringTheSameMetricTwiceWithADifferentShapePanics(t *testing.T) {
	r := NewRegistry()
	r.Counter("edumart_shared_total", "First.", "a")
	defer func() {
		if recover() == nil {
			t.Fatal("registering a counter and a gauge with one name must fail loudly")
		}
	}()
	r.Gauge("edumart_shared_total", "Second.")
}

// TestUnknownWebhookOutcomeIsCountedAsRejected keeps a typo from creating a series no alert watches.
func TestUnknownWebhookOutcomeIsCountedAsRejected(t *testing.T) {
	r := NewRegistry()
	p := NewPlatform(r)
	p.Webhook("accpeted") // transposed on purpose
	out := r.Render()
	if !strings.Contains(out, `edumart_webhook_events_total{outcome="rejected"} 1`) {
		t.Fatalf("an unknown outcome vanished instead of being counted:\n%s", out)
	}
	if strings.Contains(out, "accpeted") {
		t.Fatal("a typo became a metric label")
	}
}

// TestUnverifiedBackupIsReportedAsFailure: a heartbeat without a verification is a backup nobody restored,
// and the gauge has to say so rather than reporting success.
func TestUnverifiedBackupIsReportedAsFailure(t *testing.T) {
	r := NewRegistry()
	p := NewPlatform(r)
	path := t.TempDir() + "/heartbeat.json"
	writeFile(t, path, `{"at":"2026-10-29T02:17:00Z","engine":"postgres","rows":201,"sha256":"abc"}`)
	p.ReadBackupHeartbeat(path)
	out := r.Render()
	if !strings.Contains(out, "edumart_backup_last_run_succeeded 0") {
		t.Fatalf("an unverified backup must not be reported as a success:\n%s", out)
	}
	if !strings.Contains(out, "edumart_backup_rows 201") {
		t.Fatalf("the row count from the heartbeat is missing:\n%s", out)
	}
}

// TestAMissingHeartbeatEmitsNoBackupMetric: absence is the signal the alert uses (`absent()`), so it must
// stay absent rather than becoming a zero that looks like a very old backup of zero rows.
func TestAMissingHeartbeatEmitsNoBackupMetric(t *testing.T) {
	r := NewRegistry()
	p := NewPlatform(r)
	p.ReadBackupHeartbeat(t.TempDir() + "/does-not-exist.json")
	// Registered but unobserved: the HELP/TYPE lines may exist, but there must be no *sample*. `absent()`
	// works on series, so an emitted zero would look like a real backup of zero rows — and an old one at
	// that — instead of "no backup has ever been recorded".
	for _, line := range strings.Split(r.Render(), "\n") {
		if strings.HasPrefix(line, "edumart_backup_rows ") || strings.HasPrefix(line, "edumart_backup_age_seconds ") {
			t.Fatalf("a missing heartbeat produced a backup sample: %q", line)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func nowOrZero() time.Time { return time.Unix(1_800_000_000, 0).UTC() }

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
