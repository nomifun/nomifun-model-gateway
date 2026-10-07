// SPDX-License-Identifier: Apache-2.0
// Uses unmodified go-pay/gopay RSA2 verification/signing and merchant DTOs.
// Source: https://github.com/go-pay/gopay v1.5.115
// Commit: 4018e170ad505b9fd6e6fc2c071199579b7840a2; Apache-2.0.
package payment

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-pay/gopay"
	"github.com/go-pay/gopay/alipay"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

type alipayProvider struct {
	cfg         Config
	client      *http.Client
	privateKey  *rsa.PrivateKey
	platformKey string
}

func newAlipay(c Config, client *http.Client) (Provider, error) {
	if c.Provider != "alipay" || !configured(c) {
		return nil, ErrProviderUnavailable
	}
	k, err := parsePrivateKey(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	pub, err := normalizedPublicKey(c.PlatformPublicKey)
	if err != nil {
		return nil, err
	}
	return &alipayProvider{cfg: c, client: client, privateKey: k, platformKey: pub}, nil
}
func (*alipayProvider) Name() string   { return "alipay" }
func minorDecimal(amount int64) string { return fmt.Sprintf("%d.%02d", amount/100, amount%100) }
func decimalMinor(s string) (int64, error) {
	if s == "" || strings.TrimSpace(s) != s {
		return 0, ErrInvalidEvent
	}
	v := strings.Split(s, ".")
	if len(v) > 2 || v[0] == "" {
		return 0, ErrInvalidEvent
	}
	for _, p := range v {
		for _, c := range p {
			if c < '0' || c > '9' {
				return 0, ErrInvalidEvent
			}
		}
	}
	whole, err := strconv.ParseInt(v[0], 10, 64)
	if err != nil || whole > math.MaxInt64/100 {
		return 0, ErrInvalidEvent
	}
	frac := int64(0)
	if len(v) == 2 {
		if len(v[1]) < 1 || len(v[1]) > 2 {
			return 0, ErrInvalidEvent
		}
		f := v[1]
		if len(f) == 1 {
			f += "0"
		}
		frac, err = strconv.ParseInt(f, 10, 64)
		if err != nil {
			return 0, ErrInvalidEvent
		}
	}
	if whole*100 > math.MaxInt64-frac {
		return 0, ErrInvalidEvent
	}
	return whole*100 + frac, nil
}
func (a *alipayProvider) request(ctx context.Context, method string, biz map[string]any, out any) error {
	data, err := json.Marshal(biz)
	if err != nil {
		return ErrInvalidOrder
	}
	bm := gopay.BodyMap{"app_id": a.cfg.AppID, "method": method, "format": "JSON", "charset": "utf-8", "sign_type": "RSA2", "timestamp": time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format("2006-01-02 15:04:05"), "version": "1.0", "biz_content": string(data)}
	if method == "alipay.trade.precreate" {
		bm["notify_url"] = a.cfg.NotifyURL
	}
	sign, err := alipay.GetRsaSign(bm, alipay.RSA2, a.privateKey)
	if err != nil {
		return ErrUpstream
	}
	bm["sign"] = sign
	form := url.Values{}
	for k, v := range bm {
		form.Set(k, fmt.Sprint(v))
	}
	origin := "https://openapi.alipay.com/gateway.do"
	if a.cfg.Sandbox {
		origin = "https://openapi-sandbox.dl.alipaydev.com/gateway.do"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, origin, strings.NewReader(form.Encode()))
	if err != nil {
		return ErrUpstream
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.client.Do(req)
	if err != nil {
		return ErrUpstream
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != 200 {
		return ErrUpstream
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return ErrUpstream
	}
	key := strings.ReplaceAll(method, ".", "_") + "_response"
	body, ok := raw[key]
	if !ok {
		return ErrUpstream
	}
	var sig string
	if json.Unmarshal(raw["sign"], &sig) != nil {
		return ErrInvalidSignature
	}
	valid, err := alipay.VerifySyncSign(a.platformKey, string(body), sig)
	if err != nil || !valid {
		return ErrInvalidSignature
	}
	if json.Unmarshal(body, out) != nil {
		return ErrInvalidEvent
	}
	return nil
}
func (a *alipayProvider) CreateCheckout(ctx context.Context, o core.PaymentOrder) (Checkout, error) {
	if o.Currency != "CNY" || o.Amount <= 0 {
		return Checkout{}, ErrInvalidOrder
	}
	var v alipay.TradePrecreate
	err := a.request(ctx, "alipay.trade.precreate", map[string]any{"out_trade_no": o.ID, "total_amount": minorDecimal(o.Amount), "subject": "Gateway " + o.Kind, "seller_id": a.cfg.MerchantID, "timeout_express": "30m"}, &v)
	if err != nil {
		return Checkout{}, err
	}
	if v.Code != "10000" || v.OutTradeNo != o.ID {
		return Checkout{}, ErrUpstream
	}
	u, err := url.Parse(v.QrCode)
	if err != nil || u.Scheme != "https" || !(u.Hostname() == "qr.alipay.com" || strings.HasSuffix(u.Hostname(), ".alipay.com")) || u.User != nil {
		return Checkout{}, ErrInvalidEvent
	}
	return Checkout{ExternalID: o.ID, URL: v.QrCode}, nil
}
func (a *alipayProvider) VerifyWebhook(_ context.Context, _ http.Header, raw []byte) (Event, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return Event{}, ErrInvalidEvent
	}
	form, err := url.ParseQuery(string(raw))
	if err != nil {
		return Event{}, ErrInvalidEvent
	}
	bm := gopay.BodyMap{}
	for k, v := range form {
		if len(v) != 1 {
			return Event{}, ErrInvalidEvent
		}
		bm[k] = v[0]
	}
	if form.Get("sign_type") != "RSA2" {
		return Event{}, ErrInvalidSignature
	}
	ok, err := alipay.VerifySign(a.platformKey, bm)
	if err != nil || !ok {
		return Event{}, ErrInvalidSignature
	}
	notifyTime, err := time.ParseInLocation("2006-01-02 15:04:05", form.Get("notify_time"), time.FixedZone("Asia/Shanghai", 8*3600))
	if err != nil || !fresh(notifyTime, time.Now()) {
		return Event{}, ErrInvalidSignature
	}
	if form.Get("app_id") != a.cfg.AppID || form.Get("seller_id") != a.cfg.MerchantID || form.Get("notify_id") == "" || len(form.Get("notify_id")) > 200 {
		return Event{}, ErrInvalidEvent
	}
	amount, err := decimalMinor(form.Get("total_amount"))
	if err != nil || amount <= 0 {
		return Event{}, ErrInvalidEvent
	}
	if currency := form.Get("trans_currency"); currency != "" && currency != "CNY" {
		return Event{}, ErrInvalidEvent
	}
	state, err := alipayState(form.Get("trade_status"))
	if err != nil {
		return Event{}, err
	}
	if form.Get("out_trade_no") == "" || form.Get("trade_no") == "" {
		return Event{}, ErrInvalidEvent
	}
	return Event{ID: form.Get("notify_id"), OrderID: form.Get("out_trade_no"), ExternalID: form.Get("trade_no"), MerchantID: a.cfg.MerchantID, AppID: a.cfg.AppID, State: state, Amount: amount, Currency: "CNY", OccurredAt: notifyTime}, nil
}
func alipayState(s string) (string, error) {
	switch s {
	case "TRADE_SUCCESS", "TRADE_FINISHED":
		return "paid", nil
	case "TRADE_CLOSED":
		return "cancelled", nil
	case "WAIT_BUYER_PAY":
		return "pending", nil
	}
	return "", ErrInvalidEvent
}
func (a *alipayProvider) Query(ctx context.Context, o core.PaymentOrder) (Event, error) {
	var v alipay.TradeQuery
	if err := a.request(ctx, "alipay.trade.query", map[string]any{"out_trade_no": o.ID}, &v); err != nil {
		return Event{}, err
	}
	if v.Code != "10000" || v.OutTradeNo != o.ID || v.TradeNo == "" {
		return Event{}, ErrUpstream
	}
	amount, err := decimalMinor(v.TotalAmount)
	if err != nil {
		return Event{}, err
	}
	if v.TransCurrency != "" && v.TransCurrency != "CNY" {
		return Event{}, ErrInvalidEvent
	}
	state, err := alipayState(v.TradeStatus)
	if err != nil {
		return Event{}, err
	}
	return Event{ID: fmt.Sprintf("query:%s:%s", v.TradeNo, state), OrderID: o.ID, ExternalID: v.TradeNo, MerchantID: a.cfg.MerchantID, AppID: a.cfg.AppID, State: state, Amount: amount, Currency: "CNY", OccurredAt: time.Now()}, nil
}
