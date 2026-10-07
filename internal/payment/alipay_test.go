// SPDX-License-Identifier: Apache-2.0
package payment

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-pay/gopay"
	"github.com/go-pay/gopay/alipay"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

func aliKeys(t *testing.T) (*rsa.PrivateKey, string, string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})), string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))
}
func aliConfig(t *testing.T) (Config, *rsa.PrivateKey, *rsa.PrivateKey) {
	merchant, private, _ := aliKeys(t)
	platform, _, public := aliKeys(t)
	return Config{Provider: "alipay", Enabled: true, Sandbox: true, AppID: "2026000001", MerchantID: "2088000001", PrivateKey: private, PlatformPublicKey: public, NotifyURL: "https://operator.example/api/payments/v1/alipay/webhook"}, merchant, platform
}
func aliSign(t *testing.T, k *rsa.PrivateKey, raw string) string {
	t.Helper()
	h := sha256.Sum256([]byte(raw))
	s, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, h[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(s)
}
func aliNotification(t *testing.T, c Config, k *rsa.PrivateKey, alter func(url.Values)) []byte {
	t.Helper()
	v := url.Values{"notify_id": {"notify_fixture"}, "notify_time": {time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04:05")}, "app_id": {c.AppID}, "seller_id": {c.MerchantID}, "out_trade_no": {"0123456789abcdef0123456789abcdef"}, "trade_no": {"ali_fixture"}, "total_amount": {"123.45"}, "trade_status": {"TRADE_SUCCESS"}}
	if alter != nil {
		alter(v)
	}
	bm := gopay.BodyMap{}
	for key, values := range v {
		bm[key] = values[0]
	}
	v.Set("sign", aliSign(t, k, bm.EncodeAliPaySignParams()))
	v.Set("sign_type", "RSA2")
	return []byte(v.Encode())
}
func TestAlipayWebhookSignatureFreshnessIdentityAndDecimalAmounts(t *testing.T) {
	c, _, platform := aliConfig(t)
	p, err := newAlipay(c, newHTTPClient(nil))
	if err != nil {
		t.Fatal(err)
	}
	raw := aliNotification(t, c, platform, nil)
	e, err := p.VerifyWebhook(context.Background(), nil, raw)
	if err != nil || e.Amount != 12345 || e.State != "paid" || e.AppID != c.AppID || e.MerchantID != c.MerchantID {
		t.Fatalf("valid webhook %v %#v", err, e)
	}
	for _, tt := range []struct {
		name   string
		change func(url.Values)
	}{
		{"merchant", func(v url.Values) { v.Set("seller_id", "other") }},
		{"app", func(v url.Values) { v.Set("app_id", "other") }},
		{"currency", func(v url.Values) { v.Set("trans_currency", "USD") }},
		{"float_exponent", func(v url.Values) { v.Set("total_amount", "1e3") }},
		{"fraction_precision", func(v url.Values) { v.Set("total_amount", "123.456") }},
		{"negative", func(v url.Values) { v.Set("total_amount", "-1.00") }},
		{"overflow", func(v url.Values) { v.Set("total_amount", "92233720368547758.08") }},
		{"missing_order", func(v url.Values) { v.Del("out_trade_no") }},
		{"stale", func(v url.Values) {
			v.Set("notify_time", time.Now().Add(-6*time.Minute).In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04:05"))
		}},
		{"future", func(v url.Values) {
			v.Set("notify_time", time.Now().Add(6*time.Minute).In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04:05"))
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			b := aliNotification(t, c, platform, tt.change)
			if _, err := p.VerifyWebhook(context.Background(), nil, b); err == nil {
				t.Fatal("signed invalid event accepted")
			}
		})
	}
	form, _ := url.ParseQuery(string(raw))
	form.Set("total_amount", "123.46")
	if _, err = p.VerifyWebhook(context.Background(), nil, []byte(form.Encode())); err == nil {
		t.Fatal("altered payload accepted")
	}
	if _, err = p.VerifyWebhook(context.Background(), nil, append(raw, []byte("&seller_id=other")...)); err == nil {
		t.Fatal("duplicate form accepted")
	}
}
func TestAlipayOfficialMerchantRequestsAndSignedResponses(t *testing.T) {
	c, merchant, platform := aliConfig(t)
	order := core.PaymentOrder{ID: "0123456789abcdef0123456789abcdef", Provider: "alipay", Amount: 12345, Currency: "CNY", Kind: "wallet", ExpiresAt: time.Now().Add(time.Hour)}
	requests := 0
	client := newHTTPClient(paymentRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Scheme != "https" || r.URL.Host != "openapi-sandbox.dl.alipaydev.com" || r.URL.Path != "/gateway.do" {
			t.Fatal("non official origin")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		bm := gopay.BodyMap{}
		for key, v := range r.PostForm {
			if key != "sign" {
				bm[key] = v[0]
			}
		}
		sig, err := base64.StdEncoding.DecodeString(r.PostForm.Get("sign"))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(bm.EncodeAliPaySignParams()))
		if rsa.VerifyPKCS1v15(&merchant.PublicKey, crypto.SHA256, sum[:], sig) != nil {
			t.Fatal("request signature invalid")
		}
		var biz map[string]any
		json.Unmarshal([]byte(r.PostForm.Get("biz_content")), &biz)
		if biz["out_trade_no"] != order.ID || r.PostForm.Get("app_id") != c.AppID {
			t.Fatal("request binding")
		}
		method := r.PostForm.Get("method")
		response := `{"code":"10000","out_trade_no":"` + order.ID + `","qr_code":"https://qr.alipay.com/fixture"}`
		if method == "alipay.trade.precreate" {
			if biz["total_amount"] != "123.45" || biz["seller_id"] != c.MerchantID || r.PostForm.Get("notify_url") != c.NotifyURL {
				t.Fatal("checkout server price missing")
			}
		} else {
			response = `{"code":"10000","out_trade_no":"` + order.ID + `","trade_no":"ali_fixture","trade_status":"TRADE_SUCCESS","total_amount":"123.45"}`
		}
		key := strings.ReplaceAll(method, ".", "_") + "_response"
		body := fmt.Sprintf(`{%q:%s,"sign":%q}`, key, response, aliSign(t, platform, response))
		return paymentResponse(200, body), nil
	}))
	p, err := newAlipay(c, client)
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := p.CreateCheckout(context.Background(), order)
	if err != nil || checkout.URL != "https://qr.alipay.com/fixture" {
		t.Fatal("checkout", err)
	}
	e, err := p.Query(context.Background(), order)
	if err != nil || e.Amount != order.Amount || e.OrderID != order.ID || e.State != "paid" {
		t.Fatal("query", err)
	}
	if requests != 2 {
		t.Fatal("unexpected requests")
	}
	// Response RSA verification is required independently of TLS.
	bad := newHTTPClient(paymentRoundTrip(func(*http.Request) (*http.Response, error) {
		return paymentResponse(200, `{"alipay_trade_query_response":{"code":"10000"},"sign":"bad"}`), nil
	}))
	p, err = newAlipay(c, bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Query(context.Background(), order); err == nil {
		t.Fatal("unsigned merchant response accepted")
	}
}
func TestPaymentDecimalMinorExact(t *testing.T) {
	for _, tt := range []struct {
		raw  string
		want int64
	}{{"0.01", 1}, {"1", 100}, {"1.1", 110}, {"92233720368547758.07", 9223372036854775807}} {
		v, err := decimalMinor(tt.raw)
		if err != nil || v != tt.want {
			t.Fatalf("%s = %d %v", tt.raw, v, err)
		}
	}
	for _, raw := range []string{"", ".5", "1.", "+1", "-1", "NaN", "1e2", " 1.00", "1.000", "92233720368547758.08"} {
		if _, err := decimalMinor(raw); err == nil {
			t.Fatal("invalid decimal", raw)
		}
	}
	if got := minorDecimal(12345); got != "123.45" {
		t.Fatal(got)
	}
	_ = alipay.RSA2 // test deliberately uses the pinned merchant library
}
