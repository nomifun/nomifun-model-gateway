// SPDX-License-Identifier: Apache-2.0
package payment

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

type wxTestTransport func(*http.Request) (*http.Response, error)

func (f wxTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type wxFixture struct {
	config   Config
	merchant *rsa.PrivateKey
	platform *rsa.PrivateKey
}

func newWXFixture(t *testing.T) wxFixture {
	t.Helper()
	merchant, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	platform, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(merchant)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&platform.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return wxFixture{merchant: merchant, platform: platform, config: Config{
		Provider: "wechat", Enabled: true, AppID: "wx_test_app", MerchantID: "1234567890",
		PrivateKey:        string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})),
		PlatformPublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})),
		PlatformKeyID:     "PUB_KEY_ID_0123456789ABCDEF", CertificateSerial: "0123456789ABCDEF",
		APIV3Key: "0123456789abcdefghijklmnopqrstuv", NotifyURL: "https://gateway.example/v1/payments/wechat/webhook",
	}}
}

func wxTestOrder() core.PaymentOrder {
	return core.PaymentOrder{ID: "0123456789abcdef0123456789abcdef", Provider: "wechat", Kind: "wallet",
		Amount: 12345, Currency: "CNY", ExpiresAt: time.Now().Add(15 * time.Minute)}
}

func (f wxFixture) transaction(t *testing.T, total string) []byte {
	t.Helper()
	return []byte(fmt.Sprintf(`{"appid":%q,"mchid":%q,"out_trade_no":%q,"transaction_id":"4200000000123456789","trade_type":"NATIVE","trade_state":"SUCCESS","success_time":%q,"amount":{"total":%s,"currency":"CNY","payer_total":12345,"payer_currency":"CNY"}}`,
		f.config.AppID, f.config.MerchantID, wxTestOrder().ID, time.Now().Add(-time.Minute).Format(time.RFC3339), total))
}

func (f wxFixture) encryptedEnvelope(t *testing.T, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher([]byte(f.config.APIV3Key))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce, associated := "0123456789ab", "transaction"
	ciphertext := gcm.Seal(nil, []byte(nonce), plaintext, []byte(associated))
	body, err := json.Marshal(map[string]any{
		"id": "event_0001", "create_time": time.Now().Format(time.RFC3339),
		"event_type": "TRANSACTION.SUCCESS", "resource_type": "encrypt-resource",
		"resource": map[string]any{"algorithm": "AEAD_AES_256_GCM", "nonce": nonce,
			"associated_data": associated, "ciphertext": base64.StdEncoding.EncodeToString(ciphertext), "original_type": "transaction"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func wxTestSign(t *testing.T, key *rsa.PrivateKey, message string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(message))
	signed, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(signed)
}

func (f wxFixture) headers(t *testing.T, body []byte, timestamp time.Time) http.Header {
	t.Helper()
	stamp, nonce := strconv.FormatInt(timestamp.Unix(), 10), "test-http-nonce"
	return http.Header{"Wechatpay-Timestamp": {stamp}, "Wechatpay-Nonce": {nonce},
		"Wechatpay-Serial":    {f.config.PlatformKeyID},
		"Wechatpay-Signature": {wxTestSign(t, f.platform, stamp+"\n"+nonce+"\n"+string(body)+"\n")}}
}

func TestWechatWebhookSignatureAndDecrypt(t *testing.T) {
	f := newWXFixture(t)
	provider, err := newWechat(f.config, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := f.encryptedEnvelope(t, f.transaction(t, "12345"))
	headers := f.headers(t, body, time.Now())
	event, err := provider.VerifyWebhook(context.Background(), headers, body)
	if err != nil {
		t.Fatal(err)
	}
	if event.ID != "event_0001" || event.OrderID != wxTestOrder().ID || event.Amount != 12345 || event.Currency != "CNY" ||
		event.State != "paid" || event.ExternalID != "4200000000123456789" || event.AppID != f.config.AppID ||
		event.MerchantID != f.config.MerchantID || event.OccurredAt.IsZero() {
		t.Fatalf("unexpected verified event: %+v", event)
	}
	for _, test := range []struct {
		name string
		edit func(http.Header)
	}{
		{"wrong signature", func(h http.Header) { h.Set("Wechatpay-Signature", "ZmFrZQ==") }},
		{"wrong platform key ID", func(h http.Header) { h.Set("Wechatpay-Serial", "PUB_KEY_ID_OTHER") }},
		{"duplicate signature", func(h http.Header) { h.Add("Wechatpay-Signature", h.Get("Wechatpay-Signature")) }},
		{"missing timestamp", func(h http.Header) { h.Del("Wechatpay-Timestamp") }},
		{"noncanonical timestamp", func(h http.Header) { h.Set("Wechatpay-Timestamp", "+"+h.Get("Wechatpay-Timestamp")) }},
		{"timestamp overflow", func(h http.Header) { h.Set("Wechatpay-Timestamp", "9223372036854775808") }},
		{"wrong signature algorithm", func(h http.Header) { h.Set("Wechatpay-Signature-Type", "HMAC-SHA256") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := headers.Clone()
			test.edit(h)
			if _, err := provider.VerifyWebhook(context.Background(), h, body); !errors.Is(err, ErrInvalidSignature) {
				t.Fatalf("want invalid signature, got %v", err)
			}
		})
	}
	for _, offset := range []time.Duration{-6 * time.Minute, 6 * time.Minute} {
		h := f.headers(t, body, time.Now().Add(offset))
		if _, err := provider.VerifyWebhook(context.Background(), h, body); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("signed stale/future notification accepted: %v", err)
		}
	}
	mutated := append([]byte(nil), body...)
	mutated[len(mutated)-1] = ' '
	if _, err := provider.VerifyWebhook(context.Background(), headers, mutated); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("raw signed body was not verified: %v", err)
	}
}

func TestWechatWebhookTypedMerchantAndAmount(t *testing.T) {
	f := newWXFixture(t)
	provider, err := newWechat(f.config, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, from, to string }{
		{"wrong merchant", f.config.MerchantID, "9999999999"},
		{"wrong app", f.config.AppID, "wx_other_app"},
		{"wrong currency", `"currency":"CNY"`, `"currency":"USD"`},
		{"float amount", `"total":12345`, `"total":12345.0`},
		{"string amount", `"total":12345`, `"total":"12345"`},
		{"overflow amount", `"total":12345`, `"total":9223372036854775808`},
		{"zero amount", `"total":12345`, `"total":0`},
		{"null amount", `"total":12345`, `"total":null`},
		{"nonpaid success event", `"trade_state":"SUCCESS"`, `"trade_state":"NOTPAY"`},
		{"wrong trade type", `"trade_type":"NATIVE"`, `"trade_type":"JSAPI"`},
		{"empty transaction ID", `"transaction_id":"4200000000123456789"`, `"transaction_id":""`},
		{"invalid order ID", wxTestOrder().ID, "bad/id"},
	} {
		t.Run(test.name, func(t *testing.T) {
			plaintext := bytes.Replace(f.transaction(t, "12345"), []byte(test.from), []byte(test.to), 1)
			body := f.encryptedEnvelope(t, plaintext)
			if _, err := provider.VerifyWebhook(context.Background(), f.headers(t, body, time.Now()), body); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("want invalid event, got %v", err)
			}
		})
	}
	// Values above JavaScript's integer precision survive decoding exactly.
	body := f.encryptedEnvelope(t, f.transaction(t, "9007199254740993"))
	event, err := provider.VerifyWebhook(context.Background(), f.headers(t, body, time.Now()), body)
	if err != nil || event.Amount != 9007199254740993 {
		t.Fatalf("int64 amount lost precision: %+v %v", event, err)
	}
	for _, test := range []struct{ name, from, to string }{
		{"wrong GCM nonce length", `"nonce":"0123456789ab"`, `"nonce":"too-short"`},
		{"null encrypted resource", `"resource":{`, `"resource":null,"ignored":{`},
		{"wrong cipher", `"algorithm":"AEAD_AES_256_GCM"`, `"algorithm":"AES-CBC"`},
		{"tampered encrypted payload", `"ciphertext":"`, `"ciphertext":"AAAA`},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := bytes.Replace(f.encryptedEnvelope(t, f.transaction(t, "12345")), []byte(test.from), []byte(test.to), 1)
			if _, err := provider.VerifyWebhook(context.Background(), f.headers(t, body, time.Now()), body); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("want invalid signed envelope, got %v", err)
			}
		})
	}
}

func wxVerifyMerchantRequest(t *testing.T, f wxFixture, r *http.Request, body []byte) {
	t.Helper()
	if r.URL.Scheme != "https" || r.URL.Host != "api.mch.weixin.qq.com" {
		t.Fatalf("merchant request escaped fixed official origin: %s", r.URL)
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "WECHATPAY2-SHA256-RSA2048 ") {
		t.Fatalf("missing official authorization signature: %s", header)
	}
	fields := map[string]string{}
	for _, match := range regexp.MustCompile(`([a-z_]+)="([^"]*)"`).FindAllStringSubmatch(header, -1) {
		fields[match[1]] = match[2]
	}
	if fields["mchid"] != f.config.MerchantID || fields["serial_no"] != f.config.CertificateSerial {
		t.Fatal("wrong merchant request identity")
	}
	signature, err := base64.StdEncoding.DecodeString(fields["signature"])
	if err != nil {
		t.Fatal(err)
	}
	message := r.Method + "\n" + r.URL.RequestURI() + "\n" + fields["timestamp"] + "\n" + fields["nonce_str"] + "\n" + string(body) + "\n"
	sum := sha256.Sum256([]byte(message))
	if err := rsa.VerifyPKCS1v15(&f.merchant.PublicKey, crypto.SHA256, sum[:], signature); err != nil {
		t.Fatalf("merchant request was not signed over method, URI, and body: %v", err)
	}
}

func TestWechatNativeCheckoutAndQueryOfficialSDK(t *testing.T) {
	f := newWXFixture(t)
	order := wxTestOrder()
	var calls int
	transport := wxTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body []byte
		if r.Body != nil {
			var err error
			body, err = io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
		}
		wxVerifyMerchantRequest(t, f, r, body)
		var response []byte
		if r.Method == http.MethodPost {
			if r.URL.Path != "/v3/pay/transactions/native" {
				t.Fatalf("wrong checkout path: %s", r.URL.Path)
			}
			var payload struct {
				AppID      string `json:"appid"`
				MerchantID string `json:"mchid"`
				OrderID    string `json:"out_trade_no"`
				NotifyURL  string `json:"notify_url"`
				Expires    string `json:"time_expire"`
				Amount     struct {
					Total    int64  `json:"total"`
					Currency string `json:"currency"`
				} `json:"amount"`
			}
			if json.Unmarshal(body, &payload) != nil || payload.AppID != f.config.AppID || payload.MerchantID != f.config.MerchantID ||
				payload.OrderID != order.ID || payload.NotifyURL != f.config.NotifyURL || payload.Expires == "" ||
				payload.Amount.Total != order.Amount || payload.Amount.Currency != order.Currency {
				t.Fatalf("invalid Native payload: %s", body)
			}
			response = []byte(`{"code_url":"weixin://wxpay/bizpayurl?pr=synthetic_qr"}`)
		} else {
			if r.Method != http.MethodGet || r.URL.Path != "/v3/pay/transactions/out-trade-no/"+order.ID || r.URL.Query().Get("mchid") != f.config.MerchantID {
				t.Fatalf("wrong official query: %s %s", r.Method, r.URL)
			}
			response = f.transaction(t, "12345")
		}
		return &http.Response{StatusCode: 200, Header: f.headers(t, response, time.Now()),
			Body: io.NopCloser(bytes.NewReader(response)), Request: r}, nil
	})
	provider, err := newWechat(f.config, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := provider.CreateCheckout(context.Background(), order)
	if err != nil || checkout.ExternalID != order.ID || checkout.URL != "weixin://wxpay/bizpayurl?pr=synthetic_qr" {
		t.Fatalf("unexpected checkout: %+v %v", checkout, err)
	}
	event, err := provider.Query(context.Background(), order)
	if err != nil || event.ID != "query:"+order.ID+":paid" || event.OrderID != order.ID || event.State != "paid" || event.Amount != order.Amount {
		t.Fatalf("unexpected queried event: %+v %v", event, err)
	}
	if calls != 2 {
		t.Fatalf("unexpected official SDK request count: %d", calls)
	}
}

func TestWechatRefusesUnsignedResponseAndSandbox(t *testing.T) {
	f := newWXFixture(t)
	for _, test := range []struct {
		name string
		edit func(*Config)
	}{
		{"sandbox", func(c *Config) { c.Sandbox = true }},
		{"invalid private key", func(c *Config) { c.PrivateKey = "invalid" }},
		{"invalid public key", func(c *Config) { c.PlatformPublicKey = "invalid" }},
		{"callback query", func(c *Config) { c.NotifyURL += "?key=bad" }},
		{"missing API v3 key", func(c *Config) { c.APIV3Key = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := f.config
			test.edit(&config)
			if _, err := newWechat(config, nil); !errors.Is(err, ErrProviderUnavailable) {
				t.Fatalf("unsafe merchant configuration accepted: %v", err)
			}
		})
	}
	provider, err := newWechat(f.config, &http.Client{Transport: wxTestTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(`{"code_url":"weixin://wxpay/bizpayurl?pr=fake"}`)), Request: r}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.CreateCheckout(context.Background(), wxTestOrder()); !errors.Is(err, ErrUpstream) {
		t.Fatalf("unsigned merchant response accepted: %v", err)
	}
	config := f.config
	config.Enabled = false
	disabled, err := newWechat(config, nil)
	if err != nil {
		t.Fatalf("disabled existing merchant must still verify callbacks: %v", err)
	}
	body := f.encryptedEnvelope(t, f.transaction(t, "12345"))
	if _, err := disabled.VerifyWebhook(context.Background(), f.headers(t, body, time.Now()), body); err != nil {
		t.Fatalf("disabled merchant rejected a paid callback for an existing order: %v", err)
	}
	if _, err := disabled.CreateCheckout(context.Background(), wxTestOrder()); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("disabled merchant started a new checkout: %v", err)
	}
}

func TestWechatQueryRejectsSignedOrderMismatch(t *testing.T) {
	f := newWXFixture(t)
	for _, test := range []struct{ name, from, to string }{
		{"wrong order", wxTestOrder().ID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"wrong amount", `"total":12345`, `"total":99999`},
		{"wrong merchant", f.config.MerchantID, "9999999999"},
		{"wrong app", f.config.AppID, "wx_other_app"},
		{"wrong currency", `"currency":"CNY"`, `"currency":"USD"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider, err := newWechat(f.config, &http.Client{Transport: wxTestTransport(func(r *http.Request) (*http.Response, error) {
				body := bytes.Replace(f.transaction(t, "12345"), []byte(test.from), []byte(test.to), 1)
				return &http.Response{StatusCode: 200, Header: f.headers(t, body, time.Now()),
					Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.Query(context.Background(), wxTestOrder()); !errors.Is(err, ErrInvalidEvent) {
				t.Fatalf("signed merchant response with mismatched order accepted: %v", err)
			}
		})
	}
	for _, test := range []struct{ native, state string }{{"NOTPAY", "pending"}, {"USERPAYING", "pending"}, {"CLOSED", "cancelled"}, {"REVOKED", "cancelled"}, {"PAYERROR", "failed"}} {
		t.Run(test.native, func(t *testing.T) {
			provider, err := newWechat(f.config, &http.Client{Transport: wxTestTransport(func(r *http.Request) (*http.Response, error) {
				body := bytes.Replace(f.transaction(t, "12345"), []byte(`"trade_state":"SUCCESS"`), []byte(`"trade_state":"`+test.native+`"`), 1)
				return &http.Response{StatusCode: 200, Header: f.headers(t, body, time.Now()),
					Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			event, err := provider.Query(context.Background(), wxTestOrder())
			if err != nil || event.State != test.state || event.ID != "query:"+wxTestOrder().ID+":"+test.state || event.ExternalID != wxTestOrder().ID {
				t.Fatalf("invalid reconciliation state: %+v %v", event, err)
			}
		})
	}
}
