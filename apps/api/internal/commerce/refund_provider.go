package commerce

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/teamthy/edumart/apps/api/internal/httpx"
	"github.com/teamthy/edumart/apps/api/internal/store"
)

// RefundProvider moves money back to a customer.
//
// The interface exists so the sandbox, Paystack and a test fake are the same shape to the service — and,
// more importantly, so the *uncertainty* has a place to live. A method that can only return success or an
// error would force every caller to guess whether a failed call moved money, and guessing there is how a
// refund becomes two refunds.
type RefundProvider interface {
	Name() string
	// CreateRefund asks the provider to return money. The result says what the provider claims; an error
	// says whether that claim is knowable (see ProviderError).
	CreateRefund(ctx context.Context, request RefundRequest) (RefundResult, error)
}

// Refund states as the *provider* describes them, normalised across providers.
const (
	RefundStatusPending     = "pending"
	RefundStatusSucceeded   = "succeeded"
	RefundStatusFailed      = "failed"
	RefundStatusNeedsReview = "needs_review"
)

// RefundRequest is everything a provider needs to return money, and nothing it does not: no customer
// email, no order id, no internal reason beyond the note the provider will show on its dashboard.
type RefundRequest struct {
	// OrderReference is the reference the provider knows the payment by.
	OrderReference string
	// ProviderReference is the provider's id for the original transaction, when we have one.
	ProviderReference string
	AmountMinor       int
	Currency          string
	// Note is what a human sees in the provider's dashboard next to the refund. It is deliberately the
	// operator's reason, not the customer's details.
	Note string
}

// RefundResult is the provider's answer to a refund request.
type RefundResult struct {
	// ProviderReference is the provider's id for the refund, used to match the confirmation webhook.
	ProviderReference string
	// Status is one of the RefundStatus* constants.
	Status string
	// ProviderStatus is the provider's own word for it, kept for the audit trail.
	ProviderStatus string
}

// ProviderError distinguishes a refusal from a silence.
//
// The distinction is the whole safety property of this file. A 400 from the provider means the refund did
// not happen and nobody's money moved; a timeout means it might have. Retrying the first is free. Retrying
// the second is how a customer gets paid twice, so an ambiguous failure is never retried automatically —
// it is recorded as needs_review and a person resolves it against the provider's dashboard.
type ProviderError struct {
	// Definite is true when the provider answered and refused, so no money moved.
	Definite bool
	// Code is the provider's error code or the transport failure, for the log and the audit row.
	Code string
	// Message is safe to show an operator. It never contains the request body or the credentials.
	Message string
}

func (e *ProviderError) Error() string {
	kind := "ambiguous"
	if e.Definite {
		kind = "refused"
	}
	return fmt.Sprintf("refund provider %s: %s (%s)", kind, e.Message, e.Code)
}

// sandboxRefunds is the development stand-in. There is no external money to wait for, so it settles
// immediately — but it goes through the same lifecycle as a real provider, which is what keeps the
// pending path exercised locally and in tests.
type sandboxRefunds struct{}

func (sandboxRefunds) Name() string { return "sandbox" }

func (sandboxRefunds) CreateRefund(_ context.Context, request RefundRequest) (RefundResult, error) {
	return RefundResult{
		ProviderReference: "sbx_rf_" + strings.TrimPrefix(request.OrderReference, "edu_"),
		Status:            RefundStatusSucceeded,
		ProviderStatus:    "processed",
	}, nil
}

// PaystackRefunds is the real adapter.
//
// Two things about Paystack's refund API shape the code below:
//
//   - There is no idempotency key. The provider will happily create a second refund for the same
//     transaction, so exactly-once is the caller's job — which is why the service reserves the refund row
//     (unique idempotency key) *before* calling, and why an ambiguous outcome is never retried.
//   - The call returns as soon as the refund is accepted, usually `pending`. The money moves later, and
//     the confirmation arrives as a `refund.processed` webhook. Treating the HTTP 200 as "refunded" would
//     be the same client-side inference the payment path already refuses to make.
type PaystackRefunds struct {
	// SecretKey is the Paystack secret key. Never logged, never in an error message.
	SecretKey string
	// BaseURL is https://api.paystack.co in production and an httptest server in the tests.
	BaseURL string
	// Client is optional; the default has a timeout, because a refund call without one holds a request
	// open until the provider or the network gives up.
	Client *http.Client
}

// NewPaystackRefunds builds the adapter with a bounded HTTP client.
func NewPaystackRefunds(secretKey, baseURL string) *PaystackRefunds {
	if baseURL == "" {
		baseURL = "https://api.paystack.co"
	}
	return &PaystackRefunds{
		SecretKey: secretKey,
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Client:    &http.Client{Timeout: 20 * time.Second},
	}
}

func (p *PaystackRefunds) Name() string { return "paystack" }

// paystackRefundBody is the request Paystack expects. `amount` is in the currency's minor unit, and
// omitting it asks for a full refund — we always send it, because we always know exactly how much we mean.
type paystackRefundBody struct {
	Transaction  string `json:"transaction"`
	Amount       int    `json:"amount"`
	Currency     string `json:"currency,omitempty"`
	MerchantNote string `json:"merchant_note,omitempty"`
	CustomerNote string `json:"customer_note,omitempty"`
}

type paystackRefundResponse struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
	Data    struct {
		ID               json.Number `json:"id"`
		Status           string      `json:"status"`
		RefundedAmount   int         `json:"refunded_amount"`
		TransactionID    json.Number `json:"transaction"`
		TransactionRef   string      `json:"reference"`
		Currency         string      `json:"currency"`
		Integration      json.Number `json:"integration"`
		MerchantNote     string      `json:"merchant_note"`
		DeductedAmount   int         `json:"deducted_amount"`
		FullyDeducted    bool        `json:"fully_deducted"`
		ExpectedAt       string      `json:"expected_at"`
		ProcessedAt      string      `json:"processed_at"`
		CustomerNote     string      `json:"customer_note"`
		StatusAnnotation string      `json:"status_annotation"`
	} `json:"data"`
}

// CreateRefund asks Paystack to return money.
func (p *PaystackRefunds) CreateRefund(ctx context.Context, request RefundRequest) (RefundResult, error) {
	if p.SecretKey == "" {
		return RefundResult{}, &ProviderError{
			Definite: true,
			Code:     "no_credentials",
			Message:  "the payment provider secret key is not configured, so no refund was attempted",
		}
	}
	// We key the refund on the provider's transaction id when we have one and the order reference
	// otherwise: both are identifiers Paystack accepts, and the reference is what the payment was created
	// with, so it is always available.
	transaction := request.ProviderReference
	if transaction == "" {
		transaction = request.OrderReference
	}
	body, err := json.Marshal(paystackRefundBody{
		Transaction:  transaction,
		Amount:       request.AmountMinor,
		MerchantNote: request.Note,
		CustomerNote: request.Note,
	})
	if err != nil {
		return RefundResult{}, &ProviderError{Definite: true, Code: "encode", Message: "the refund request could not be encoded"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+"/refund", bytes.NewReader(body))
	if err != nil {
		return RefundResult{}, &ProviderError{Definite: true, Code: "request", Message: "the refund request could not be built"}
	}
	req.Header.Set("Authorization", "Bearer "+p.SecretKey)
	req.Header.Set("Content-Type", "application/json")

	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		// No answer at all. The refund may or may not exist on their side; that is precisely the case
		// that must not be retried blind.
		return RefundResult{}, &ProviderError{
			Definite: false,
			Code:     "transport",
			Message:  "the provider did not answer, so the outcome of the refund is unknown",
		}
	}
	defer res.Body.Close()
	// A provider error body is a few kilobytes at most; capping it keeps a hostile or broken response from
	// becoming a memory problem.
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))

	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		return RefundResult{}, &ProviderError{Definite: false, Code: "rate_limited",
			Message: "the provider rate-limited the refund, so it may or may not have been created"}
	case res.StatusCode >= 500:
		return RefundResult{}, &ProviderError{Definite: false, Code: fmt.Sprintf("http_%d", res.StatusCode),
			Message: "the provider failed while creating the refund, so the outcome is unknown"}
	case res.StatusCode >= 400:
		// A 4xx is the provider saying no: bad amount, unknown transaction, insufficient balance. No money
		// moved, so this is safe to record as a failure.
		return RefundResult{}, &ProviderError{
			Definite: true,
			Code:     fmt.Sprintf("http_%d", res.StatusCode),
			Message:  "the provider refused the refund: " + paystackMessage(raw),
		}
	}

	var parsed paystackRefundResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return RefundResult{}, &ProviderError{Definite: false, Code: "unparseable",
			Message: "the provider answered with something that is not valid JSON, so the outcome is unknown"}
	}
	if !parsed.Status {
		return RefundResult{}, &ProviderError{
			Definite: true,
			Code:     "status_false",
			Message:  "the provider refused the refund: " + paystackMessage(raw),
		}
	}

	result := RefundResult{
		ProviderReference: parsed.Data.ID.String(),
		Status:            mapPaystackRefundStatus(parsed.Data.Status),
		ProviderStatus:    parsed.Data.Status,
	}
	if result.ProviderReference == "" || result.ProviderReference == "0" {
		// Without their id we cannot match the confirmation webhook, so treat the attempt as ambiguous
		// rather than settled: an un-matchable success is worse than a flagged one.
		return RefundResult{}, &ProviderError{Definite: false, Code: "no_reference",
			Message: "the provider accepted the refund without an id, so it cannot be matched to its confirmation"}
	}
	return result, nil
}

// mapPaystackRefundStatus normalises the provider's vocabulary. An unknown status is deliberately treated
// as "not yet done": the safe direction is to leave the money effects unapplied until a confirmation
// arrives, because applying them early is what hands out a refund and an entitlement revocation for money
// that is still in the account.
func mapPaystackRefundStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "processed", "success", "successful", "completed":
		return RefundStatusSucceeded
	case "failed", "declined", "reversed":
		return RefundStatusFailed
	case "pending", "processing", "queued", "needs-attention", "needs_attention", "":
		return RefundStatusPending
	}
	return RefundStatusPending
}

func paystackMessage(raw []byte) string {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Message != "" {
		return payload.Message
	}
	return "no reason given"
}

// ─────────────────────────── webhook normalisation ───────────────────────────

// PaystackWebhookSecret is the key used to sign Paystack's `x-paystack-signature` header: HMAC-SHA512 of
// the raw request body, hex encoded. It is the *secret key*, not a separate webhook secret.
type PaystackWebhook struct {
	SecretKey string
}

// VerifySignature checks x-paystack-signature over the raw body.
//
// Over the raw bytes, not a re-serialised struct: re-encoding a parsed body reorders and reformats it, so a
// signature check against the re-encoded form would either always fail or, worse, be checkable against
// attacker-controlled normalisation.
func (p PaystackWebhook) VerifySignature(rawBody []byte, signature string) bool {
	if p.SecretKey == "" || signature == "" {
		return false
	}
	mac := hmac.New(sha512.New, []byte(p.SecretKey))
	mac.Write(rawBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(strings.TrimSpace(signature))))
}

// paystackEvent is the envelope Paystack posts: `{"event": "charge.success", "data": {...}}`.
type paystackEvent struct {
	Event string `json:"event"`
	Data  struct {
		ID          json.Number `json:"id"`
		Reference   string      `json:"reference"`
		Status      string      `json:"status"`
		Amount      int         `json:"amount"`
		Currency    string      `json:"currency"`
		GatewayResp string      `json:"gateway_response"`
		PaidAt      string      `json:"paid_at"`
		CreatedAt   string      `json:"created_at"`
		// Refund events carry the transaction the money came from, either as an object or as an id.
		Transaction json.RawMessage `json:"transaction"`
	} `json:"data"`
}

// Normalise turns a Paystack event into the internal WebhookEvent.
//
// This exists so the rest of the system never sees a provider payload. The alternative — teaching the
// payment handler every provider's envelope — is how a second provider becomes a rewrite.
func (p PaystackWebhook) Normalise(rawBody []byte) (WebhookEvent, bool, error) {
	var envelope paystackEvent
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		return WebhookEvent{}, false, fmt.Errorf("paystack payload is not JSON: %w", err)
	}
	event := WebhookEvent{
		ID:         strings.TrimSpace(envelope.Event) + ":" + envelope.Data.ID.String(),
		Currency:   strings.ToUpper(envelope.Data.Currency),
		OccurredAt: StringOr(envelope.Data.PaidAt, envelope.Data.CreatedAt),
	}
	if event.ID == ":" {
		return WebhookEvent{}, false, errors.New("paystack event has neither a type nor an id")
	}

	switch strings.ToLower(envelope.Event) {
	case "charge.success":
		event.Type = "payment.succeeded"
		event.Reference = envelope.Data.Reference
		event.AmountMinor = envelope.Data.Amount
		event.Status = "succeeded"
		return event, true, nil
	case "charge.failed", "charge.dispute.create":
		event.Type = "payment.failed"
		event.Reference = envelope.Data.Reference
		event.AmountMinor = envelope.Data.Amount
		event.Status = "failed"
		return event, true, nil
	case "refund.processed", "refund.pending", "refund.processing", "refund.failed", "refund.needs-attention":
		event.Type = "refund." + refundEventSuffix(envelope.Event)
		event.ProviderRefundReference = envelope.Data.ID.String()
		event.Reference = refundTransactionReference(envelope.Data.Transaction)
		event.AmountMinor = envelope.Data.Amount
		event.Status = mapPaystackRefundStatus(StringOr(envelope.Data.Status, refundEventSuffix(envelope.Event)))
		return event, true, nil
	}
	// A valid, signed event we do not act on. Recorded and ignored rather than rejected: a rejection would
	// make Paystack retry it forever.
	return event, false, nil
}

func refundEventSuffix(event string) string {
	switch strings.ToLower(event) {
	case "refund.processed":
		return "succeeded"
	case "refund.failed", "refund.needs-attention":
		return "failed"
	}
	return "pending"
}

// refundTransactionReference pulls the order reference out of a refund event's `transaction`, which is an
// object on some events and an id on others.
func refundTransactionReference(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asObject struct {
		Reference string `json:"reference"`
	}
	if err := json.Unmarshal(raw, &asObject); err == nil && asObject.Reference != "" {
		return asObject.Reference
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	return ""
}

// StringOr returns the first non-empty string.
func StringOr(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// HandleProviderWebhook accepts a provider-shaped callback.
//
// The generic endpoint speaks our own signed envelope. A real provider does not, so each provider gets a
// small translator here rather than a fork of the money logic: verify with *their* scheme, normalise into a
// WebhookEvent, and hand it to the same applyEvent the generic path uses. Anything unrecognised is rejected
// rather than guessed at.
func (s *Service) HandleProviderWebhook(ctx context.Context, provider string, rawBody []byte, signature string) (map[string]any, error) {
	if !strings.EqualFold(provider, "paystack") {
		// Not a provider-shaped callback: the generic signed envelope is the contract.
		return s.HandleWebhook(ctx, rawBody, signature)
	}
	if s.paystackWebhook == nil {
		// A live provider is talking to us and we have no key to check it with. Refusing is the only honest
		// answer: accepting the body because we cannot verify it would make the endpoint forgeable.
		s.webhookOutcome("rejected")
		s.logError("paystack webhook received but no signing secret is configured", "event", provider)
		return nil, httpx.NewError(httpx.CodeForbidden, "Invalid webhook signature.")
	}
	if !s.paystackWebhook.VerifySignature(rawBody, signature) {
		s.webhookOutcome("rejected")
		return nil, httpx.NewError(httpx.CodeForbidden, "Invalid webhook signature.")
	}
	event, handled, err := s.paystackWebhook.Normalise(rawBody)
	if err != nil {
		s.webhookOutcome("rejected")
		return nil, httpx.NewError(httpx.CodeValidation, "Malformed webhook payload.")
	}
	payloadHash := sha256Hex(rawBody)
	if !handled {
		// Signed, valid, and not something we act on. Recorded and acknowledged: rejecting would make the
		// provider retry it forever, and this endpoint is one of the few an unauthenticated caller can reach.
		if err := s.recordIgnoredEvent(ctx, event, payloadHash); err != nil {
			return nil, err
		}
		s.webhookOutcome("ignored")
		return map[string]any{"received": true, "handled": false}, nil
	}
	result, err := s.applyEvent(ctx, event, payloadHash)
	if err != nil {
		return nil, err
	}
	result["handled"] = true
	return result, nil
}

// recordIgnoredEvent stores an event we verified but do not act on, so "the provider is sending us
// something we ignore" is answerable from the database rather than from a provider's dashboard.
func (s *Service) recordIgnoredEvent(ctx context.Context, event WebhookEvent, payloadHash string) error {
	_, err := s.db.DB.ExecContext(ctx, `
		INSERT INTO provider_events (id, provider, event_id, signature_valid, processing_status, payload_hash, received_at)
		VALUES (?,?,?,1,'ignored_status',?,?) ON CONFLICT (provider, event_id) DO NOTHING`,
		store.NewID(), s.provider, event.ID, payloadHash, store.Now())
	return err
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
