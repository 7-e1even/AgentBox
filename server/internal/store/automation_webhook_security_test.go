package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"testing"
	"time"

	"agentbox/internal/platform"
)

func TestWebhookTimestampFreshnessRejectsExtremeFutureWithoutDurationOverflow(t *testing.T) {
	now := time.Date(2026, time.September, 3, 0, 0, 0, 0, time.UTC)
	if webhookTimestampIsFresh(1<<63-1, now) {
		t.Fatal("maximum int64 timestamp was accepted as fresh")
	}
	if !webhookTimestampIsFresh(now.Add(webhookSignatureWindow).Unix(), now) {
		t.Fatal("timestamp at the positive freshness boundary was rejected")
	}
	if webhookTimestampIsFresh(now.Add(webhookSignatureWindow+time.Second).Unix(), now) {
		t.Fatal("timestamp beyond the positive freshness boundary was accepted")
	}
}

func TestSignedWebhookModesRejectExtremeFutureTimestamp(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	secret := "0123456789abcdef0123456789abcdef"
	ciphertext, nonce, err := encryptSecret(key, secret)
	if err != nil {
		t.Fatal(err)
	}
	serverStore := &Store{secretKey: key}
	timestamp := strconv.FormatInt(1<<63-1, 10)
	body := []byte(`{"event":"test"}`)

	tests := []struct {
		name     string
		authMode platform.AutomationAuthMode
		delivery platform.AutomationDelivery
	}{
		{
			name:     "HMAC SHA-256",
			authMode: platform.AutomationAuthHMAC,
			delivery: platform.AutomationDelivery{Timestamp: timestamp, Body: body},
		},
		{
			name:     "Standard Webhooks",
			authMode: platform.AutomationAuthStandardWebhook,
			delivery: platform.AutomationDelivery{Body: body, Headers: map[string]string{
				"webhook-id": "delivery-one", "webhook-timestamp": timestamp,
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mac := hmac.New(sha256.New, []byte(secret))
			if test.authMode == platform.AutomationAuthHMAC {
				_, _ = mac.Write([]byte(timestamp + "."))
				_, _ = mac.Write(body)
				test.delivery.Signature = "v1=" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
			} else {
				_, _ = mac.Write([]byte("delivery-one." + timestamp + "."))
				_, _ = mac.Write(body)
				test.delivery.Headers["webhook-signature"] = "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
			}
			stored := storedAutomation{
				Automation:       platform.Automation{Trigger: platform.AutomationTriggerInput{AuthMode: test.authMode}},
				SecretCiphertext: ciphertext,
				SecretNonce:      nonce,
			}
			if err := serverStore.verifyAutomationDelivery(stored, test.delivery); !errors.Is(err, ErrWebhookUnauthorized) {
				t.Fatalf("extreme future timestamp error = %v, want ErrWebhookUnauthorized", err)
			}
		})
	}
}
