package metrics

import (
	"encoding/json"
	"os"
	"time"
)

// Platform is the set of metrics this product needs an alert to be able to see. It exists as a type
// rather than a bag of package-level variables so that a test can build a fresh one, and so that the
// alert rules have one place to be checked against.
type Platform struct {
	registry *Registry

	errors         *Handler
	webhooks       *Handler
	refundOutcomes *Handler
	refunds        *Handler
	refundedMinor  *Handler
	reconcileKind  *Handler
	reconcileRun   *Handler
	reconcilePaid  *Handler
	backupAge      *Handler
	backupRows     *Handler
	backupOK       *Handler
	loginFailures  *Handler
}

// NewPlatform registers every product metric. Registering all of them up front — even the ones that stay
// at zero — is what makes `absent()` a meaningful alert: a metric that only appears once something has
// happened cannot be distinguished from a service that is not reporting at all.
func NewPlatform(registry *Registry) *Platform {
	return &Platform{
		registry: registry,
		errors: registry.Counter("edumart_api_errors_total",
			"Refusals and failures by API error code. 5xx codes here are the ones worth waking someone for.",
			"code"),
		webhooks: registry.Counter("edumart_webhook_events_total",
			"Payment webhook events by outcome. `rejected` means the signature or the payload was wrong.",
			"outcome"),
		refundOutcomes: registry.Counter("edumart_refund_outcomes_total",
			"Refund requests by outcome. `needs_review` means the provider's answer was ambiguous and a person must check it.",
			"outcome"),
		refunds: registry.Counter("edumart_refunds_total",
			"Refunds recorded, by currency.", "currency"),
		refundedMinor: registry.Counter("edumart_refunded_minor_total",
			"Money returned, in the currency's minor unit.", "currency"),
		reconcileKind: registry.Gauge("edumart_reconciliation_discrepancies",
			"Contradictions found by the last reconciliation, by kind.", "kind"),
		reconcileRun: registry.Gauge("edumart_reconciliation_last_run_timestamp_seconds",
			"Unix time of the last reconciliation report. Absent means it has never run."),
		reconcilePaid: registry.Gauge("edumart_reconciliation_paid_orders",
			"Paid orders seen by the last reconciliation report."),
		backupAge: registry.Gauge("edumart_backup_age_seconds",
			"Age of the last successful, verified backup. This is the metric the backup alert watches."),
		backupRows: registry.Gauge("edumart_backup_rows",
			"Rows in the last successful backup, as counted when it was taken."),
		backupOK: registry.Gauge("edumart_backup_last_run_succeeded",
			"1 when the last backup run succeeded, 0 when it failed. A failed run is the state that must page."),
		loginFailures: registry.Counter("edumart_login_failures_total",
			"Failed sign-in attempts. A spike is either credential stuffing or a broken client."),
	}
}

// Registry returns the underlying registry, for the scrape endpoint.
func (p *Platform) Registry() *Registry { return p.registry }

// CountError satisfies httpx.ErrorRecorder.
func (p *Platform) CountError(code string) { p.errors.With(code).Inc() }

// Webhook counts an outcome: accepted, duplicate, rejected (bad signature or payload), ignored.
func (p *Platform) Webhook(outcome string) {
	switch outcome {
	case "accepted", "duplicate", "rejected", "ignored":
		p.webhooks.With(outcome).Inc()
	default:
		// An outcome the alert rules do not know about would be invisible. Count it as rejected rather
		// than inventing a series nobody alerts on.
		p.webhooks.With("rejected").Inc()
	}
}

// RefundOutcome counts what happened to a refund request.
//
// The four states answer different questions: succeeded is money out, failed is a provider refusal (a
// support problem), pending is normal, and needs_review is a person's job — the provider did not answer
// clearly and nobody is allowed to guess. Only the last two are worth waking someone for, and this counter
// is what makes that possible.
func (p *Platform) RefundOutcome(outcome string) {
	switch outcome {
	case "succeeded", "failed", "pending", "needs_review":
	default:
		outcome = "unknown"
	}
	p.refundOutcomes.With(outcome).Inc()
}

// Refund records money going back and how much of it.
func (p *Platform) Refund(currency string, amountMinor int) {
	p.refunds.With(currency).Inc()
	p.refundedMinor.With(currency).Add(float64(amountMinor))
}

// ReconciliationReport publishes the result of a reconciliation run. The gauges are set, not incremented:
// they answer "what does the books say now", and a cumulative count of discrepancies would only ever grow.
func (p *Platform) ReconciliationReport(at time.Time, paidOrders int, discrepancies map[string]int) {
	p.reconcileRun.Set(float64(at.Unix()))
	p.reconcilePaid.Set(float64(paidOrders))
	for _, kind := range []string{
		"paid_without_entitlement", "entitlement_without_payment", "refund_exceeds_total",
		"succeeded_attempt_unpaid", "paid_without_attempt",
	} {
		p.reconcileKind.With(kind).Set(float64(discrepancies[kind]))
	}
}

// LoginFailure counts a refused sign-in.
func (p *Platform) LoginFailure() { p.loginFailures.Inc() }

// BackupHeartbeat is the file the backup job writes after a backup has been taken *and verified*. It is
// the difference between "a cron job is configured" and "a usable backup exists", which is the only
// question an alert should be answering.
type BackupHeartbeat struct {
	At       string `json:"at"`
	Engine   string `json:"engine"`
	File     string `json:"file"`
	Rows     int    `json:"rows"`
	SHA256   string `json:"sha256"`
	Verified *bool  `json:"verifiedAt,omitempty"`
}

// ReadBackupHeartbeat loads the heartbeat file. A missing file is not an error the API should fail on —
// it is the honest answer "no backup has ever been recorded", which the alert handles with `absent()`.
func (p *Platform) ReadBackupHeartbeat(path string) {
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var beat BackupHeartbeat
	if err := json.Unmarshal(raw, &beat); err != nil {
		return
	}
	at, err := time.Parse(time.RFC3339, beat.At)
	if err != nil {
		return
	}
	p.backupAge.Set(time.Since(at).Seconds())
	p.backupRows.Set(float64(beat.Rows))
	if beat.Verified != nil && *beat.Verified {
		p.backupOK.Set(1)
	} else {
		// A backup nobody restored is a hypothesis; the gauge says so rather than pretending.
		p.backupOK.Set(0)
	}
}
