package wallet

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChargeProvider starts a hosted checkout for a top-up.
//
// The interface exists for the same reason the refund provider's does: a sandbox, a real provider and a test
// fake have to be the same shape to the service, so the *uncertainty* — did the money move? — has one place to
// live instead of leaking into the wallet's credit logic. The service never parses a provider payload; it asks
// the provider what a webhook body means.
type ChargeProvider interface {
	// Name is stored on the top-up row, because "which provider took this money" is a question a finance
	// reconciliation asks months later, when configuration has changed.
	Name() string
	// Checkout asks the provider for a payment page. Nothing is credited here: a checkout is a request for
	// money, not a receipt of it.
	Checkout(ctx context.Context, request ChargeRequest) (ChargeResult, error)
	// VerifySignature reports whether a webhook body really came from the provider.
	VerifySignature(rawBody []byte, signature string) bool
	// ParseSettlement turns a verified webhook body into a settlement, or reports that the event is not a
	// settlement at all (an event type this service does not act on).
	ParseSettlement(rawBody []byte) (Settlement, bool, error)
}

// ChargeRequest is everything a provider needs to open a payment page.
type ChargeRequest struct {
	// Reference is our own unique reference, and it is what the webhook will quote back. It is generated
	// before the provider call so a payment that arrives before the HTTP response does still match a row.
	Reference   string
	AmountMinor int
	Currency    string
	// Email is the payer's address. Paystack requires one; it is not used for marketing and is not stored
	// on the top-up row.
	Email string
	// CallbackURL is where the provider sends the browser after payment. It returns to the wallet, which
	// then re-reads the balance — it never marks anything paid, because only a signed webhook may do that.
	CallbackURL string
}

// ChargeResult is the provider's answer to a checkout request.
type ChargeResult struct {
	// CheckoutURL is where to send the browser. Empty means the provider has no hosted page (sandbox).
	CheckoutURL string
	// ProviderReference is the provider's id for this charge, kept for support and reconciliation.
	ProviderReference string
}

// Settlement is a verified statement that money moved.
type Settlement struct {
	Reference   string
	AmountMinor int
	Currency    string
	// Succeeded is false for a failure event: the row is closed, nothing is credited.
	Succeeded bool
	// ProviderStatus is the provider's own word, kept for the audit trail.
	ProviderStatus string
	// EventID deduplicates provider retries. It is the provider's event identity, not ours.
	EventID string
}

// PaystackCharges is the live adapter: Paystack's hosted checkout plus its `charge.success` webhook.
//
// Three details shape this code, and each of them is a way this goes wrong:
//
//  1. **The reference is ours.** Paystack accepts a `reference` on initialize and echoes it on the webhook.
//     Generating it here means the top-up row exists before the payment page does, so a webhook that arrives
//     while the browser is still loading still finds a row to settle.
//  2. **The signature is HMAC-SHA512 over the raw body.** Re-serialising a parsed body reorders keys, so a
//     check against the re-encoded form either always fails or checks attacker-chosen normalisation. The
//     signature is the *secret key*, so the secret must be a live key in production and a test key elsewhere.
//  3. **The amount in the webhook is the authority for the credit, but not for the price.** The credit is
//     computed from the top-up row's stored rate, and the webhook's amount must equal the row's amount. A
//     mismatch is refused rather than credited at either figure, because both choices are a way to lose money.
type PaystackCharges struct {
	// SecretKey is the Paystack secret key. Never logged, never in an error message.
	SecretKey string
	// BaseURL is https://api.paystack.co in production, and an httptest server in the tests.
	BaseURL string
	// Client is optional. The default has a timeout: a checkout call that hangs is a topped-up learner who
	// sees a spinner instead of a payment page.
	Client *http.Client
}

// Name implements ChargeProvider.
func (p PaystackCharges) Name() string { return "paystack" }

func (p PaystackCharges) client() *http.Client {
	if p.Client != nil {
		return p.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (p PaystackCharges) baseURL() string {
	if strings.TrimSpace(p.BaseURL) != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return "https://api.paystack.co"
}

// ErrProviderNotConfigured is returned when a provider is asked to take money without the credentials to do
// it. It is a definite failure: nothing was sent, so nothing moved.
var ErrProviderNotConfigured = errors.New("payment provider is not configured")

// Checkout implements ChargeProvider.
func (p PaystackCharges) Checkout(ctx context.Context, request ChargeRequest) (ChargeResult, error) {
	if strings.TrimSpace(p.SecretKey) == "" {
		return ChargeResult{}, ErrProviderNotConfigured
	}
	if request.Email == "" {
		// Paystack refuses an initialize without a customer email. Failing here names the reason instead of
		// turning a malformed request into "the payment provider is broken".
		return ChargeResult{}, errors.New("a payment needs the payer's email address")
	}

	body := map[string]any{
		"email":     request.Email,
		"amount":    request.AmountMinor,
		"currency":  request.Currency,
		"reference": request.Reference,
		// Metadata is what makes a Paystack dashboard row legible to support: it carries our ids, not the
		// payer's details.
		"metadata": map[string]any{"product": "edumart_wallet_topup", "reference": request.Reference},
	}
	if request.CallbackURL != "" {
		body["callback_url"] = request.CallbackURL
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return ChargeResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL()+"/transaction/initialize", bytes.NewReader(payload))
	if err != nil {
		return ChargeResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client().Do(req)
	if err != nil {
		// The transport failure is not echoed: it can contain the URL, and the URL never contains secrets but
		// the error is not useful to a learner either.
		return ChargeResult{}, fmt.Errorf("paystack initialize: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ChargeResult{}, fmt.Errorf("paystack initialize: reading the response failed: %w", err)
	}

	var parsed struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			AuthorizationURL string `json:"authorization_url"`
			AccessCode       string `json:"access_code"`
			Reference        string `json:"reference"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ChargeResult{}, &ProviderError{Definite: false, Code: "unparseable",
			Message: "the payment provider answered with something that is not JSON"}
	}
	if resp.StatusCode >= 400 || !parsed.Status {
		// A 4xx that the provider explained is a refusal: nothing was charged, and retrying cannot double-charge.
		message := parsed.Message
		if message == "" {
			message = fmt.Sprintf("the payment provider refused the request (%d)", resp.StatusCode)
		}
		return ChargeResult{}, &ProviderError{Definite: resp.StatusCode < 500, Code: "initialize_refused",
			Message: message}
	}
	if parsed.Data.AuthorizationURL == "" {
		return ChargeResult{}, &ProviderError{Definite: false, Code: "no_checkout_url",
			Message: "the payment provider accepted the request without a checkout URL"}
	}
	return ChargeResult{
		CheckoutURL:       parsed.Data.AuthorizationURL,
		ProviderReference: parsed.Data.AccessCode,
	}, nil
}

// VerifySignature checks `x-paystack-signature`: HMAC-SHA512 of the raw body, hex encoded, keyed with the
// secret key.
func (p PaystackCharges) VerifySignature(rawBody []byte, signature string) bool {
	if strings.TrimSpace(p.SecretKey) == "" || signature == "" {
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
		ID        json.Number `json:"id"`
		Reference string      `json:"reference"`
		Status    string      `json:"status"`
		Amount    int         `json:"amount"`
		Currency  string      `json:"currency"`
	} `json:"data"`
}

// ParseSettlement implements ChargeProvider.
//
// Only two event families matter to a wallet: a charge that succeeded and a charge that failed. Everything
// else — transfers, subscriptions, disputes — is explicitly not a top-up settlement, and returning `false`
// keeps the caller from inventing meaning for it.
func (p PaystackCharges) ParseSettlement(rawBody []byte) (Settlement, bool, error) {
	var envelope paystackEvent
	if err := json.Unmarshal(rawBody, &envelope); err != nil {
		return Settlement{}, false, fmt.Errorf("paystack payload is not JSON: %w", err)
	}
	event := strings.ToLower(strings.TrimSpace(envelope.Event))
	settlement := Settlement{
		Reference:      strings.TrimSpace(envelope.Data.Reference),
		AmountMinor:    envelope.Data.Amount,
		Currency:       strings.ToUpper(strings.TrimSpace(envelope.Data.Currency)),
		ProviderStatus: envelope.Data.Status,
		EventID:        event + ":" + envelope.Data.ID.String(),
	}
	switch event {
	case "charge.success":
		settlement.Succeeded = true
		return settlement, true, nil
	case "charge.failed":
		settlement.Succeeded = false
		return settlement, true, nil
	}
	return Settlement{}, false, nil
}

// String keeps the secret out of a formatted value, which is the difference between a log line and an
// incident.
func (p PaystackCharges) String() string {
	return fmt.Sprintf("paystack(base=%s, key=%s)", p.baseURL(), maskSecret(p.SecretKey))
}

func maskSecret(secret string) string {
	if len(secret) <= 4 {
		return "***"
	}
	return secret[:4] + "***"
}

// ProviderError distinguishes a refusal from a silence, exactly as the refund adapter does. A 4xx means the
// provider declined and no money moved; a timeout means the checkout may exist, so the top-up is left open
// rather than failed — closing it would strand a payment page a learner might already be looking at.
type ProviderError struct {
	// Definite is true when the provider answered and refused.
	Definite bool
	Code     string
	Message  string
}

func (e *ProviderError) Error() string {
	kind := "ambiguous"
	if e.Definite {
		kind = "refused"
	}
	return fmt.Sprintf("payment provider %s: %s (%s)", kind, e.Message, e.Code)
}

// SandboxCharges is the development stand-in: there is no hosted page and no money, so the top-up stays
// pending until the sandbox settle route closes it. It exists so the same code path runs locally, and so the
// wallet keeps working on a laptop with no provider account.
type SandboxCharges struct{}

// Name implements ChargeProvider.
func (SandboxCharges) Name() string { return "sandbox" }

// Checkout implements ChargeProvider.
func (SandboxCharges) Checkout(context.Context, ChargeRequest) (ChargeResult, error) {
	return ChargeResult{}, nil
}

// VerifySignature implements ChargeProvider. The sandbox has no webhook: self-confirmation is a session-
// authenticated route, not a signed one.
func (SandboxCharges) VerifySignature([]byte, string) bool { return false }

// ParseSettlement implements ChargeProvider.
func (SandboxCharges) ParseSettlement([]byte) (Settlement, bool, error) {
	return Settlement{}, false, nil
}
