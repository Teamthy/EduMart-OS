package commerce

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// providerServer is a stand-in for Paystack's refund endpoint. The adapter's job is to turn whatever it
// answers into one of four states, so the tests drive each of those answers directly.
func providerServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)
	return server.URL
}

// TestPaystackSendsWhatTheProviderExpects: the body is the contract with the provider, so it is asserted
// literally rather than through a struct round-trip that could drift with it.
func TestPaystackSendsWhatTheProviderExpects(t *testing.T) {
	var got struct {
		Body          map[string]any
		Authorization string
		Method        string
	}
	url := providerServer(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got.Body)
		got.Authorization = r.Header.Get("Authorization")
		got.Method = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":true,"message":"Refund initiated","data":{"id":302,"status":"pending"}}`))
	})

	provider := NewPaystackRefunds("sk_test_secret", url)
	result, err := provider.CreateRefund(context.Background(), RefundRequest{
		OrderReference: "edu_abc123", AmountMinor: 250000, Currency: "NGN", Note: "duplicate charge",
	})
	if err != nil {
		t.Fatalf("create refund: %v", err)
	}
	if got.Method != http.MethodPost || got.Authorization != "Bearer sk_test_secret" {
		t.Fatalf("unexpected request: %s %q", got.Method, got.Authorization)
	}
	if got.Body["transaction"] != "edu_abc123" || got.Body["amount"] != float64(250000) {
		t.Fatalf("unexpected body: %v", got.Body)
	}
	if result.ProviderReference != "302" || result.Status != RefundStatusPending {
		t.Fatalf("an accepted-but-pending refund must stay pending, got %+v", result)
	}
}

// TestPaystackMapsItsStatuses: the provider's vocabulary is theirs; the lifecycle is ours.
func TestPaystackMapsItsStatuses(t *testing.T) {
	cases := []struct {
		provider string
		want     string
	}{
		{"pending", RefundStatusPending},
		{"processing", RefundStatusPending},
		{"processed", RefundStatusSucceeded},
		{"failed", RefundStatusFailed},
		// An unknown status is treated as "not done yet": the safe direction, because applying the money
		// effects early is what hands out a refund for money still in the account.
		{"something-new", RefundStatusPending},
		{"", RefundStatusPending},
	}
	for _, testCase := range cases {
		if got := mapPaystackRefundStatus(testCase.provider); got != testCase.want {
			t.Errorf("mapPaystackRefundStatus(%q) = %q, want %q", testCase.provider, got, testCase.want)
		}
	}
}

// TestPaystackDistinguishesARefusalFromASilence is the whole safety property of the adapter. A refusal means
// no money moved and the refund can be retried; a silence means it might have, and retrying is how a
// customer gets paid twice.
func TestPaystackDistinguishesARefusalFromASilence(t *testing.T) {
	cases := []struct {
		name     string
		handler  func(w http.ResponseWriter, r *http.Request)
		definite bool
	}{
		{
			name: "a 4xx is a refusal",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"status":false,"message":"Transaction has been fully refunded"}`))
			},
			definite: true,
		},
		{
			name: "a 5xx is a silence",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			},
			definite: false,
		},
		{
			name: "a rate limit is a silence",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
			},
			definite: false,
		},
		{
			name: "an unparseable body is a silence",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`<html>gateway timeout</html>`))
			},
			definite: false,
		},
		{
			name: "status:false is a refusal",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"status":false,"message":"Insufficient balance"}`))
			},
			definite: true,
		},
		{
			name: "an accepted refund with no id cannot be matched, so it is a silence",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"status":true,"data":{"status":"pending"}}`))
			},
			definite: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := NewPaystackRefunds("sk_test", providerServer(t, testCase.handler))
			_, err := provider.CreateRefund(context.Background(), RefundRequest{
				OrderReference: "edu_1", AmountMinor: 1000, Currency: "NGN",
			})
			if err == nil {
				t.Fatal("expected an error")
			}
			var providerErr *ProviderError
			if !errors.As(err, &providerErr) {
				t.Fatalf("expected a ProviderError, got %T: %v", err, err)
			}
			if providerErr.Definite != testCase.definite {
				t.Fatalf("definite = %t, want %t (%v)", providerErr.Definite, testCase.definite, err)
			}
			if providerErr.Code == "" || providerErr.Message == "" {
				t.Fatalf("an operator needs a code and a message: %+v", providerErr)
			}
			if containsSecret(providerErr.Message) {
				t.Fatalf("the error message leaked the secret key: %s", providerErr.Message)
			}
		})
	}
}

// TestPaystackTimeoutIsAmbiguous: the network giving up is not the provider saying no.
func TestPaystackTimeoutIsAmbiguous(t *testing.T) {
	url := providerServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte(`{"status":true,"data":{"id":1,"status":"pending"}}`))
	})
	provider := NewPaystackRefunds("sk_test", url)
	provider.Client = &http.Client{Timeout: 20 * time.Millisecond}

	_, err := provider.CreateRefund(context.Background(), RefundRequest{OrderReference: "edu_1", AmountMinor: 100})
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Definite {
		t.Fatalf("a timeout must be an ambiguous ProviderError, got %v", err)
	}
}

// TestPaystackWithoutAKeyMovesNoMoney: a misconfigured deployment must fail loudly and safely rather than
// silently calling a provider with an empty credential.
func TestPaystackWithoutAKeyMovesNoMoney(t *testing.T) {
	provider := NewPaystackRefunds("", "https://api.paystack.co")
	_, err := provider.CreateRefund(context.Background(), RefundRequest{OrderReference: "edu_1", AmountMinor: 100})
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || !providerErr.Definite {
		t.Fatalf("an unconfigured provider must be a definite refusal, got %v", err)
	}
}

func containsSecret(message string) bool {
	return len(message) > 0 && (contains(message, "sk_test") || contains(message, "sk_live"))
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// signPaystack produces the header Paystack sends: HMAC-SHA512 of the raw body, hex encoded.
func signPaystack(secret string, body []byte) string {
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// TestPaystackSignatureIsCheckedOverTheRawBody: re-serialising a parsed body would make the check either
// always fail or pass on attacker-normalised input.
func TestPaystackSignatureIsCheckedOverTheRawBody(t *testing.T) {
	webhook := PaystackWebhook{SecretKey: "sk_test_signature"}
	body := []byte(`{"event":"charge.success","data":{"id":302961,"reference":"edu_1","amount":50000,"currency":"NGN"}}`)

	if !webhook.VerifySignature(body, signPaystack("sk_test_signature", body)) {
		t.Fatal("a correctly signed body was refused")
	}
	if webhook.VerifySignature(body, signPaystack("sk_test_wrong", body)) {
		t.Fatal("a signature from the wrong key was accepted")
	}
	// A byte changed after signing, which is what an attacker does.
	tampered := []byte(`{"event":"charge.success","data":{"id":302961,"reference":"edu_1","amount":1,"currency":"NGN"}}`)
	if webhook.VerifySignature(tampered, signPaystack("sk_test_signature", body)) {
		t.Fatal("a tampered body was accepted")
	}
	// A different, equally valid encoding of the same bytes must not verify: the check is over bytes.
	if webhook.VerifySignature(body, "") {
		t.Fatal("an empty signature was accepted")
	}
	if (PaystackWebhook{}).VerifySignature(body, signPaystack("", body)) {
		t.Fatal("an unconfigured secret must refuse every webhook")
	}
}

// TestPaystackEventsAreNormalised: the rest of the system must never see a provider payload, so this is where
// the provider's vocabulary stops.
func TestPaystackEventsAreNormalised(t *testing.T) {
	webhook := PaystackWebhook{SecretKey: "sk"}
	cases := []struct {
		name       string
		body       string
		wantType   string
		wantRef    string
		wantStatus string
		handled    bool
	}{
		{
			name:     "charge.success becomes payment.succeeded",
			body:     `{"event":"charge.success","data":{"id":1,"reference":"edu_1","amount":50000,"currency":"NGN","status":"success"}}`,
			wantType: "payment.succeeded", wantRef: "edu_1", wantStatus: "succeeded", handled: true,
		},
		{
			name:     "refund.processed becomes a settlement",
			body:     `{"event":"refund.processed","data":{"id":77,"status":"processed","amount":25000,"currency":"NGN","transaction":{"reference":"edu_1"}}}`,
			wantType: "refund.succeeded", wantRef: "edu_1", wantStatus: RefundStatusSucceeded, handled: true,
		},
		{
			name:     "refund.failed becomes a failure",
			body:     `{"event":"refund.failed","data":{"id":78,"status":"failed","amount":25000,"transaction":"edu_2"}}`,
			wantType: "refund.failed", wantRef: "edu_2", wantStatus: RefundStatusFailed, handled: true,
		},
		{
			name:     "an event we do not act on is ignored, not rejected",
			body:     `{"event":"transfer.success","data":{"id":9}}`,
			wantType: "", handled: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			event, handled, err := webhook.Normalise([]byte(testCase.body))
			if err != nil {
				t.Fatalf("normalise: %v", err)
			}
			if handled != testCase.handled {
				t.Fatalf("handled = %t, want %t", handled, testCase.handled)
			}
			if !handled {
				return
			}
			if event.Type != testCase.wantType {
				t.Fatalf("type = %q, want %q", event.Type, testCase.wantType)
			}
			if event.Reference != testCase.wantRef {
				t.Fatalf("reference = %q, want %q", event.Reference, testCase.wantRef)
			}
			if event.Status != testCase.wantStatus {
				t.Fatalf("status = %q, want %q", event.Status, testCase.wantStatus)
			}
			if event.ID == "" {
				t.Fatal("every normalised event needs an id, or duplicates cannot be detected")
			}
		})
	}
}
