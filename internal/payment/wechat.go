// SPDX-License-Identifier: Apache-2.0
package payment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nomifun/nomifun-model-gateway/internal/core"
	wxcore "github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/validators"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

var (
	wechatOrderID           = regexp.MustCompile(`^[A-Za-z0-9_-]{6,32}$`)
	wechatAppID             = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	wechatMerchantID        = regexp.MustCompile(`^[0-9]{6,32}$`)
	wechatCertificateSerial = regexp.MustCompile(`^[A-Fa-f0-9]{1,128}$`)
	wechatPlatformKeyID     = regexp.MustCompile(`^PUB_KEY_ID_[A-Fa-f0-9]{1,128}$`)
)

type wechatProvider struct {
	config    Config
	native    native.NativeApiService
	notify    *notify.Handler
	validator *validators.WechatPayNotifyValidator
}

// newWechat uses the official SDK's pinned public-key mode. It deliberately
// does not download platform keys or allow merchant origins to be configured.
// WeChat API v3 Native has no sandbox adapter: signed local fixtures exercise
// its protocol without suggesting that a simulated charge is a merchant one.
func newWechat(cfg Config, client *http.Client) (Provider, error) {
	if cfg.Provider != "wechat" || cfg.Sandbox || !configured(cfg) {
		return nil, ErrProviderUnavailable
	}
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("%w: invalid WeChat configuration", ErrProviderUnavailable)
	}
	u, err := url.Parse(cfg.NotifyURL)
	if err != nil || u.RawQuery != "" || u.ForceQuery || len(cfg.NotifyURL) > 255 ||
		!wechatAppID.MatchString(cfg.AppID) || !wechatMerchantID.MatchString(cfg.MerchantID) ||
		!wechatCertificateSerial.MatchString(cfg.CertificateSerial) || !wechatPlatformKeyID.MatchString(cfg.PlatformKeyID) {
		return nil, fmt.Errorf("%w: invalid WeChat identity or callback", ErrProviderUnavailable)
	}
	privateKey, err := utils.LoadPrivateKey(cfg.PrivateKey)
	if err != nil || privateKey.N.BitLen() < 2048 {
		return nil, fmt.Errorf("%w: invalid WeChat merchant RSA key", ErrProviderUnavailable)
	}
	publicKey, err := utils.LoadPublicKey(cfg.PlatformPublicKey)
	if err != nil || publicKey.N.BitLen() < 2048 {
		return nil, fmt.Errorf("%w: invalid WeChat platform RSA key", ErrProviderUnavailable)
	}
	// Rebuild the HTTP client using only the injectable transport. Timeout and
	// redirect policy are invariant even when a package test supplies a client.
	var transport http.RoundTripper
	if client != nil {
		transport = client.Transport
	}
	wxClient, err := wxcore.NewClient(context.Background(),
		option.WithWechatPayPublicKeyAuthCipher(cfg.MerchantID, cfg.CertificateSerial,
			privateKey, cfg.PlatformKeyID, publicKey),
		option.WithHTTPClient(newHTTPClient(transport)))
	if err != nil {
		return nil, fmt.Errorf("%w: initialize WeChat SDK", ErrProviderUnavailable)
	}
	verifier := verifiers.NewSHA256WithRSAPubkeyVerifier(cfg.PlatformKeyID, *publicKey)
	handler, err := notify.NewRSANotifyHandler(cfg.APIV3Key, verifier)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid WeChat API v3 key", ErrProviderUnavailable)
	}
	return &wechatProvider{config: cfg, native: native.NativeApiService{Client: wxClient},
		notify: handler, validator: validators.NewWechatPayNotifyValidator(verifier)}, nil
}

func (*wechatProvider) Name() string { return "wechat" }

func (p *wechatProvider) validOrder(o core.PaymentOrder) bool {
	return o.Provider == "wechat" && wechatOrderID.MatchString(o.ID) && o.Amount > 0 && o.Currency == "CNY" &&
		(o.AppID == "" || o.AppID == p.config.AppID) && (o.MerchantID == "" || o.MerchantID == p.config.MerchantID)
}

func (p *wechatProvider) CreateCheckout(ctx context.Context, o core.PaymentOrder) (Checkout, error) {
	if !p.config.Enabled {
		return Checkout{}, ErrProviderUnavailable
	}
	if !p.validOrder(o) || (!o.ExpiresAt.IsZero() && !o.ExpiresAt.After(time.Now())) {
		return Checkout{}, ErrInvalidOrder
	}
	description := "Model token credit"
	if o.Kind == "subscription" {
		description = "Model token plan"
	}
	req := native.PrepayRequest{Appid: &p.config.AppID, Mchid: &p.config.MerchantID,
		Description: &description, OutTradeNo: &o.ID, NotifyUrl: &p.config.NotifyURL,
		Amount: &native.Amount{Total: &o.Amount, Currency: &o.Currency}}
	if !o.ExpiresAt.IsZero() {
		req.TimeExpire = &o.ExpiresAt
	}
	response, _, err := p.native.Prepay(ctx, req)
	if err != nil {
		// SDK errors can contain credential-bearing request objects. Preserve a
		// stable public error and never include those details in an API response.
		return Checkout{}, fmt.Errorf("%w: WeChat native checkout", ErrUpstream)
	}
	if response == nil || response.CodeUrl == nil || !validWechatCodeURL(*response.CodeUrl) {
		return Checkout{}, ErrInvalidEvent
	}
	return Checkout{ExternalID: o.ID, URL: *response.CodeUrl}, nil
}

func validWechatCodeURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "weixin" && u.Host == "wxpay" && u.Path == "/bizpayurl" &&
		u.User == nil && u.Fragment == "" && u.Query().Get("pr") != "" && len(raw) <= 2048
}

func (p *wechatProvider) VerifyWebhook(ctx context.Context, headers http.Header, body []byte) (Event, error) {
	if len(body) == 0 || len(body) > 1<<20 {
		return Event{}, ErrInvalidEvent
	}
	// Check raw canonical headers before the SDK (which trims values). This
	// makes the signed timestamp/nonce exactly the header bytes we received.
	for _, name := range []string{"Wechatpay-Timestamp", "Wechatpay-Nonce", "Wechatpay-Signature", "Wechatpay-Serial"} {
		values := headers.Values(name)
		if len(values) != 1 || values[0] == "" || strings.TrimSpace(values[0]) != values[0] || strings.ContainsAny(values[0], "\r\n") {
			return Event{}, ErrInvalidSignature
		}
	}
	if headers.Get("Wechatpay-Serial") != p.config.PlatformKeyID || len(headers.Get("Wechatpay-Nonce")) > 256 {
		return Event{}, ErrInvalidSignature
	}
	stamp := headers.Get("Wechatpay-Timestamp")
	seconds, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != stamp || !fresh(time.Unix(seconds, 0), time.Now()) {
		return Event{}, ErrInvalidSignature
	}
	if values := headers.Values("Wechatpay-Signature-Type"); len(values) > 1 || (len(values) == 1 && values[0] != "WECHATPAY2-SHA256-RSA2048") {
		return Event{}, ErrInvalidSignature
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.config.NotifyURL, bytes.NewReader(body))
	if err != nil {
		return Event{}, ErrInvalidEvent
	}
	req.Header = headers.Clone()
	if p.validator.Validate(ctx, req) != nil {
		return Event{}, ErrInvalidSignature
	}
	// The SDK assumes resource is non-nil and GCM panics for a wrong nonce
	// length. Validate the signed envelope before passing it to the decryptor.
	var envelope notify.Request
	if json.Unmarshal(body, &envelope) != nil || envelope.ID == "" || len(envelope.ID) > 200 ||
		envelope.Resource == nil || envelope.CreateTime == nil || envelope.ResourceType != "encrypt-resource" ||
		envelope.Resource.Algorithm != "AEAD_AES_256_GCM" || len(envelope.Resource.Nonce) != 12 ||
		envelope.Resource.OriginalType != "transaction" {
		return Event{}, ErrInvalidEvent
	}
	if envelope.EventType != "TRANSACTION.SUCCESS" {
		return Event{}, ErrIgnoredEvent
	}
	var transaction payments.Transaction
	notification, err := p.notify.ParseNotifyRequest(ctx, req, &transaction)
	if err != nil || notification == nil {
		return Event{}, ErrInvalidEvent
	}
	event, err := p.transactionEvent(&transaction)
	if err != nil || event.State != "paid" {
		return Event{}, ErrInvalidEvent
	}
	event.ID = notification.ID
	return event, nil
}

func (p *wechatProvider) Query(ctx context.Context, o core.PaymentOrder) (Event, error) {
	if !p.validOrder(o) {
		return Event{}, ErrInvalidOrder
	}
	transaction, _, err := p.native.QueryOrderByOutTradeNo(ctx, native.QueryOrderByOutTradeNoRequest{
		OutTradeNo: &o.ID, Mchid: &p.config.MerchantID})
	if err != nil {
		return Event{}, fmt.Errorf("%w: WeChat order query", ErrUpstream)
	}
	event, err := p.transactionEvent(transaction)
	if err != nil || event.OrderID != o.ID || event.Amount != o.Amount || event.Currency != o.Currency {
		return Event{}, ErrInvalidEvent
	}
	event.ID = "query:" + event.OrderID + ":" + event.State
	return event, nil
}

func (p *wechatProvider) transactionEvent(t *payments.Transaction) (Event, error) {
	if t == nil || t.Appid == nil || *t.Appid != p.config.AppID || t.Mchid == nil || *t.Mchid != p.config.MerchantID ||
		t.OutTradeNo == nil || !wechatOrderID.MatchString(*t.OutTradeNo) || t.Amount == nil || t.Amount.Total == nil ||
		*t.Amount.Total <= 0 || t.Amount.Currency == nil || *t.Amount.Currency != "CNY" || t.TradeState == nil {
		return Event{}, ErrInvalidEvent
	}
	e := Event{OrderID: *t.OutTradeNo, ExternalID: *t.OutTradeNo, AppID: *t.Appid, MerchantID: *t.Mchid, Amount: *t.Amount.Total,
		Currency: *t.Amount.Currency}
	switch *t.TradeState {
	case "SUCCESS":
		if t.TransactionId == nil || *t.TransactionId == "" || len(*t.TransactionId) > 100 ||
			t.SuccessTime == nil || t.TradeType == nil || *t.TradeType != "NATIVE" {
			return Event{}, ErrInvalidEvent
		}
		occurred, err := time.Parse(time.RFC3339, *t.SuccessTime)
		if err != nil || occurred.IsZero() || occurred.After(time.Now().Add(5*time.Minute)) {
			return Event{}, ErrInvalidEvent
		}
		e.State, e.ExternalID, e.OccurredAt = "paid", *t.TransactionId, occurred
	case "NOTPAY", "USERPAYING":
		e.State = "pending"
	case "CLOSED", "REVOKED":
		e.State = "cancelled"
	case "PAYERROR":
		e.State = "failed"
	default:
		return Event{}, ErrInvalidEvent
	}
	return e, nil
}
