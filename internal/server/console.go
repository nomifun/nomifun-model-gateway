// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/payment"
	"github.com/nomifun/nomifun-model-gateway/internal/relay"
	"github.com/nomifun/nomifun-model-gateway/internal/store"
)

func (s *Server) consoleRoutes() {
	public := s.engine.Group("/api/console/v1")
	public.GET("/config", s.publicConfig)
	public.POST("/login", s.login)
	public.POST("/register", s.register)
	auth := public.Group("", s.sessionAuth(false))
	auth.POST("/logout", s.logout)
	auth.GET("/me", s.me)
	auth.GET("/catalog", s.consoleCatalog)
	auth.GET("/keys", s.keys)
	auth.POST("/keys", s.createKey)
	auth.DELETE("/keys/:id", s.revokeKey)
	auth.GET("/usage", s.usage)
	auth.GET("/plans", s.plans)
	auth.GET("/orders", s.orders)
	auth.POST("/orders", s.createOrder)
	auth.GET("/orders/:id", s.order)
	auth.POST("/redeem", s.redeem)
	admin := public.Group("/admin", s.sessionAuth(true))
	s.adminRoutes(admin)
}
func (s *Server) publicConfig(c *gin.Context) {
	settings, err := s.instance(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	providers, err := s.Payment.Providers(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	items := []gin.H{}
	for _, provider := range providers {
		names := map[string]string{"stripe": "Stripe", "alipay": "Alipay", "wechat": "WeChat Pay"}
		items = append(items, gin.H{"id": provider.Provider, "name": names[provider.Provider], "enabled": provider.Available, "currencies": provider.Currencies})
	}
	c.JSON(200, gin.H{"meta": settings.meta(), "registration_enabled": settings.RegistrationEnabled, "payment_providers": items, "currency": settings.Currency})
}
func (s *Server) authRate(c *gin.Context) bool {
	retry, ok := s.limits.authIP(remoteIP(c.Request))
	if !ok {
		c.Header("Retry-After", retrySeconds(retry))
		relay.WriteError(c.Writer, c.Request, &relay.Error{Status: 429, Code: "rate_limited", Message: "Too many authentication attempts. Try again later."}, "")
		c.Abort()
	}
	return ok
}

type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name,omitempty"`
}

func (s *Server) login(c *gin.Context) {
	if !s.authRate(c) {
		return
	}
	var in loginInput
	if !s.decode(c, &in) {
		return
	}
	u, token, err := s.Store.Login(c.Request.Context(), in.Email, in.Password)
	if err != nil {
		s.fail(c, store.ErrUnauthorized)
		return
	}
	c.JSON(200, gin.H{"user": u, "session_token": token})
}
func (s *Server) register(c *gin.Context) {
	if !s.authRate(c) {
		return
	}
	settings, err := s.instance(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	if !settings.RegistrationEnabled {
		s.fail(c, &relay.Error{Status: 403, Code: "registration_disabled", Message: "Registration is disabled by this operator."})
		return
	}
	var in loginInput
	if !s.decode(c, &in) {
		return
	}
	_, err = s.Store.Register(c.Request.Context(), in.Email, in.Name, in.Password, settings.Currency)
	if err != nil {
		s.fail(c, err)
		return
	}
	u, token, err := s.Store.Login(c.Request.Context(), in.Email, in.Password)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(201, gin.H{"user": u, "session_token": token})
}
func (s *Server) logout(c *gin.Context) {
	if s.databaseError(c, s.Store.RevokeSession(c.Request.Context(), c.MustGet("session").(string))) {
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
func (s *Server) me(c *gin.Context) {
	account, err := s.Billing.Account(c.Request.Context(), user(c).ID, 0)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, gin.H{"user": user(c), "account": account})
}
func (s *Server) consoleCatalog(c *gin.Context) {
	catalog, err := s.Store.CatalogForKey(c.Request.Context(), core.APIKey{UserID: user(c).ID, ModelIDsJSON: "[]"})
	if s.databaseError(c, err) {
		return
	}
	settings, err := s.instance(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	catalog.Models = filterCatalog(catalog.Models, settings.Capabilities)
	c.JSON(200, catalog)
}

type keyDTO struct {
	core.APIKey
	ModelIDs   []string `json:"model_ids"`
	AllowedIPs []string `json:"allowed_ips"`
}

func keyView(key core.APIKey) keyDTO {
	models := []string{}
	ips := []string{}
	_ = json.Unmarshal([]byte(key.ModelIDsJSON), &models)
	_ = json.Unmarshal([]byte(key.AllowedIPsJSON), &ips)
	return keyDTO{APIKey: key, ModelIDs: models, AllowedIPs: ips}
}
func (s *Server) keys(c *gin.Context) {
	keys, err := s.Store.ListAPIKeys(c.Request.Context(), user(c).ID)
	if s.databaseError(c, err) {
		return
	}
	out := []keyDTO{}
	for _, key := range keys {
		out = append(out, keyView(key))
	}
	c.JSON(200, gin.H{"items": out})
}
func (s *Server) createKey(c *gin.Context) {
	var in store.KeyInput
	if !s.decode(c, &in) {
		return
	}
	key, plain, err := s.Store.CreateAPIKey(c.Request.Context(), user(c).ID, in)
	if err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "api_key.create", strconvID(key.ID)) {
		return
	}
	c.JSON(201, gin.H{"key": keyView(key), "api_key": plain})
}
func (s *Server) revokeKey(c *gin.Context) {
	id, ok := s.id(c)
	if !ok {
		return
	}
	if err := s.Store.RevokeAPIKey(c.Request.Context(), user(c).ID, id); err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "api_key.revoke", strconvID(id)) {
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
func (s *Server) usage(c *gin.Context) {
	items, err := s.Store.ListUsage(c.Request.Context(), user(c).ID, 100)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) plans(c *gin.Context) {
	items, err := s.Store.ListPlans(c.Request.Context(), true)
	if s.databaseError(c, err) {
		return
	}
	out := []planDTO{}
	for _, plan := range items {
		out = append(out, planView(plan))
	}
	c.JSON(200, gin.H{"items": out})
}
func (s *Server) orders(c *gin.Context) {
	items, err := s.Payment.ListOrders(c.Request.Context(), user(c).ID, 100)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) createOrder(c *gin.Context) {
	var in payment.CreateOrderRequest
	if !s.decode(c, &in) {
		return
	}
	order, err := s.Payment.CreateOrder(c.Request.Context(), user(c).ID, in)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(201, gin.H{"order": order})
}
func (s *Server) order(c *gin.Context) {
	order, err := s.Payment.GetOrder(c.Request.Context(), user(c).ID, c.Param("id"))
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(200, gin.H{"order": order})
}
func (s *Server) redeem(c *gin.Context) {
	var in struct {
		Code string `json:"code"`
	}
	if !s.decode(c, &in) {
		return
	}
	if len(in.Code) > 200 {
		s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "Invalid redeem code."})
		return
	}
	code, err := s.Billing.Redeem(c.Request.Context(), user(c).ID, in.Code)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true, "redemption": code})
}

func (s *Server) webhook(c *gin.Context) {
	var body []byte
	var err error
	controller := http.NewResponseController(c.Writer)
	_ = controller.SetReadDeadline(time.Now().Add(60 * time.Second))
	reader := http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	body, err = io.ReadAll(reader)
	_ = controller.SetReadDeadline(time.Time{})
	if err != nil {
		s.fail(c, &relay.Error{Status: 413, Code: "request_too_large", Message: "Payment notification exceeds the body limit."})
		return
	}
	_, err = s.Payment.HandleWebhook(c.Request.Context(), c.Param("provider"), c.Request.Header, body)
	if err != nil && !errors.Is(err, payment.ErrIgnoredEvent) {
		status := 500
		if errors.Is(err, payment.ErrInvalidSignature) || errors.Is(err, payment.ErrInvalidEvent) || errors.Is(err, payment.ErrInvalidOrder) || errors.Is(err, payment.ErrOrderNotFound) || errors.Is(err, payment.ErrProviderUnavailable) {
			status = 400
		}
		s.fail(c, &relay.Error{Status: status, Code: "invalid_payment_notification", Message: "Payment notification could not be verified or applied."})
		return
	}
	switch c.Param("provider") {
	case "alipay":
		c.String(200, "success")
	case "wechat":
		c.JSON(200, gin.H{"code": "SUCCESS", "message": "success"})
	default:
		c.JSON(200, gin.H{"received": true})
	}
}

type planDTO struct {
	core.Plan
	ModelIDs []string `json:"model_ids"`
}

func planView(plan core.Plan) planDTO {
	models := []string{}
	_ = json.Unmarshal([]byte(plan.ModelIDsJSON), &models)
	return planDTO{Plan: plan, ModelIDs: models}
}

type planInput struct {
	Name       string   `json:"name"`
	Price      int64    `json:"price"`
	Currency   string   `json:"currency"`
	PeriodDays int64    `json:"period_days"`
	TokenQuota *int64   `json:"token_quota"`
	ModelIDs   []string `json:"model_ids"`
	Enabled    bool     `json:"enabled"`
}

func (in planInput) plan(id int64) core.Plan {
	if in.ModelIDs == nil {
		in.ModelIDs = []string{}
	}
	b, _ := json.Marshal(in.ModelIDs)
	return core.Plan{ID: id, Name: in.Name, Price: in.Price, Currency: in.Currency, PeriodDays: in.PeriodDays, TokenQuota: in.TokenQuota, ModelIDsJSON: string(b), Enabled: in.Enabled}
}
