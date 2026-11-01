package wallet_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/teamthy/edumart/apps/api/internal/wallet"
)

// fakePaystack serves the one endpoint the adapter calls, and records what it was asked for.
func fakePaystack(t *testing.T, status int, body string) (*httptest.Server, *map[string]any, *http.Header) {
	t.Helper()
	var received map[string]any
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transaction/initialize" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		headers = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &received)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server, &received, &headers
}

// TestCheckoutSendsTheReferenceAndTheAmount: the reference must be ours, because the webhook matches on it, and
// the amount must be in kobo, because that is the unit the top-up row stores.
func TestCheckoutSendsTheReferenceAndTheAmount(t *testing.T) {
	server, received, headers := fakePaystack(t, http.StatusOK,
		`{"status":true,"data":{"authorization_url":"https://checkout.paystack.test/abc","access_code":"ac_1","reference":"topup_1"}}`)

	provider := wallet.PaystackCharges{SecretKey: "sk_test_123", BaseURL: server.URL}
	result, err := provider.Checkout(context.Background(), wallet.ChargeRequest{
		Reference:   "topup_1",
		AmountMinor: 20000,
		Currency:    "NGN",
		Email:       "learner@example.com",
		CallbackURL: "http://localhost:3000/wallet",
	})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if result.CheckoutURL != "https://checkout.paystack.test/abc" {
		t.Fatalf("checkout url not returned: %+v", result)
	}
	if (*headers).Get("Authorization") != "Bearer sk_test_123" {
		t.Fatalf("the secret key must be sent as a bearer token, got %q", (*headers).Get("Authorization"))
	}
	if (*received)["reference"] != "topup_1" || (*received)["amount"].(float64) != 20000 {
		t.Fatalf("initialize body must carry our reference and the kobo amount, got %v", *received)
	}
	if (*received)["callback_url"] != "http://localhost:3000/wallet" {
		t.Fatalf("callback_url must return the browser to the wallet, got %v", (*received)["callback_url"])
	}
}

// TestCheckoutRefusesADefiniteFailureAndFlagsAnAmbiguousOne: the distinction decides whether a top-up row is
// closed or left open, and closing a row somebody could still pay takes money and credits nothing.
func TestCheckoutFailureClassification(t *testing.T) {
	refusing, _, _ := fakePaystack(t, http.StatusBadRequest, `{"status":false,"message":"Invalid amount"}`)
	_, err := (wallet.PaystackCharges{SecretKey: "sk", BaseURL: refusing.URL}).
		Checkout(context.Background(), wallet.ChargeRequest{Reference: "r", AmountMinor: 20000, Email: "a@b.test"})
	var providerErr *wallet.ProviderError
	if !asProviderError(err, &providerErr) || !providerErr.Definite {
		t.Fatalf("a 4xx must be a definite refusal, got %v", err)
	}
	if providerErr.Message != "Invalid amount" {
		t.Fatalf("the provider's own message must survive, got %q", providerErr.Message)
	}

	broken, _, _ := fakePaystack(t, http.StatusInternalServerError, `<html>gateway</html>`)
	_, err = (wallet.PaystackCharges{SecretKey: "sk", BaseURL: broken.URL}).
		Checkout(context.Background(), wallet.ChargeRequest{Reference: "r", AmountMinor: 20000, Email: "a@b.test"})
	if !asProviderError(err, &providerErr) || providerErr.Definite {
		t.Fatalf("a 5xx with no JSON must be ambiguous, got %v", err)
	}

	// No key means nothing was sent, so nothing moved: a definite failure, not a retry.
	_, err = (wallet.PaystackCharges{}).
		Checkout(context.Background(), wallet.ChargeRequest{Reference: "r", AmountMinor: 20000, Email: "a@b.test"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a provider with no key must refuse without calling out, got %v", err)
	}
}

func TestCheckoutNeedsThePayersEmail(t *testing.T) {
	provider := wallet.PaystackCharges{SecretKey: "sk_test_123"}
	if _, err := provider.Checkout(context.Background(),
		wallet.ChargeRequest{Reference: "r", AmountMinor: 20000}); err == nil {
		t.Fatal("an initialize without an email must fail before the request is sent")
	}
}

func signPaystack(secret string, body []byte) string {
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// TestWebhookSignatureIsCheckedOverTheRawBody: re-serialising a parsed body reorders and reformats it, so a
// check against the re-encoded form either always fails or checks attacker-chosen normalisation.
func TestWebhookSignatureIsCheckedOverTheRawBody(t *testing.T) {
	provider := wallet.PaystackCharges{SecretKey: "sk_test_signature"}
	body := []byte(`{"event":"charge.success","data":{"id":1,"reference":"topup_1","amount":20000,"currency":"NGN","status":"success"}}`)

	if !provider.VerifySignature(body, signPaystack("sk_test_signature", body)) {
		t.Fatal("a correctly signed body must verify")
	}
	if provider.VerifySignature(body, signPaystack("sk_test_wrong", body)) {
		t.Fatal("a body signed with another key must not verify")
	}
	if provider.VerifySignature([]byte(`{"event":"charge.success"}`), signPaystack("sk_test_signature", body)) {
		t.Fatal("a tampered body must not verify")
	}
	if provider.VerifySignature(body, "") {
		t.Fatal("a missing signature must not verify")
	}
	if (wallet.PaystackCharges{}).VerifySignature(body, signPaystack("", body)) {
		t.Fatal("a provider with no key must not verify anything")
	}
}

// TestParseSettlementOnlyUnderstandsCharges: an event this service does not act on must be reported as such,
// not interpreted, so nothing downstream invents a credit out of a transfer notification.
func TestParseSettlementOnlyUnderstandsCharges(t *testing.T) {
	provider := wallet.PaystackCharges{SecretKey: "sk"}

	settled, ok, err := provider.ParseSettlement([]byte(`{"event":"charge.success","data":{"id":9,"reference":"topup_9","amount":20000,"currency":"ngn","status":"success"}}`))
	if err != nil || !ok {
		t.Fatalf("charge.success must be a settlement: ok=%v err=%v", ok, err)
	}
	if !settled.Succeeded || settled.Reference != "topup_9" || settled.AmountMinor != 20000 ||
		settled.Currency != "NGN" || settled.EventID != "charge.success:9" {
		t.Fatalf("settlement not normalised: %+v", settled)
	}

	failed, ok, err := provider.ParseSettlement([]byte(`{"event":"charge.failed","data":{"id":10,"reference":"topup_10","amount":20000}}`))
	if err != nil || !ok || failed.Succeeded {
		t.Fatalf("charge.failed must settle the row as failed: %+v ok=%v err=%v", failed, ok, err)
	}

	if _, ok, _ := provider.ParseSettlement([]byte(`{"event":"transfer.success","data":{"id":11}}`)); ok {
		t.Fatal("a transfer is not a top-up settlement")
	}
	if _, _, err := provider.ParseSettlement([]byte(`not json`)); err == nil {
		t.Fatal("a body that is not JSON must be an error")
	}
}

// TestSandboxProviderTakesNoMoney keeps the development adapter honest: no checkout page, no signed webhook —
// the sandbox settles through the authenticated route only.
func TestSandboxProviderTakesNoMoney(t *testing.T) {
	var sandbox wallet.ChargeProvider = wallet.SandboxCharges{}
	if sandbox.Name() != "sandbox" {
		t.Fatalf("sandbox adapter must name itself, got %q", sandbox.Name())
	}
	result, err := sandbox.Checkout(context.Background(), wallet.ChargeRequest{})
	if err != nil || result.CheckoutURL != "" {
		t.Fatalf("the sandbox has no hosted page: %+v err=%v", result, err)
	}
	if sandbox.VerifySignature([]byte(`{}`), "deadbeef") {
		t.Fatal("the sandbox must not accept a signed webhook")
	}
}

func asProviderError(err error, target **wallet.ProviderError) bool {
	providerErr, ok := err.(*wallet.ProviderError)
	if ok {
		*target = providerErr
	}
	return ok
}
