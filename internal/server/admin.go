// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
	"github.com/nomifun/nomifun-model-gateway/internal/payment"
	"github.com/nomifun/nomifun-model-gateway/internal/relay"
	"github.com/nomifun/nomifun-model-gateway/internal/store"
	"gorm.io/gorm"
)

func (s *Server) adminRoutes(admin *gin.RouterGroup) {
	admin.GET("/channels", s.channels)
	admin.POST("/channels", s.createChannel)
	admin.PATCH("/channels/:id", s.updateChannel)
	admin.DELETE("/channels/:id", s.deleteChannel)
	admin.POST("/channels/:id/probe", s.probeChannel)
	admin.POST("/channels/:id/discover", s.discoverChannel)
	admin.POST("/channels/discover-preview", s.discoverChannelPreview)
	admin.GET("/models", s.adminModels)
	admin.POST("/models", s.saveModel)
	admin.POST("/models/check", s.checkModelPublication)
	admin.PATCH("/models/:id", s.saveModel)
	admin.DELETE("/models/:id", s.disableModel)
	admin.POST("/onboarding/check", s.checkOnboarding)
	admin.POST("/onboarding", s.createOnboarding)
	admin.GET("/pricing", s.adminModels)
	admin.PUT("/pricing/:id", s.savePricing)
	admin.GET("/plans", s.adminPlans)
	admin.POST("/plans", s.savePlan)
	admin.PATCH("/plans/:id", s.savePlan)
	admin.GET("/users", s.users)
	admin.PATCH("/users/:id", s.updateUser)
	admin.POST("/users/:id/credit", s.credit)
	admin.GET("/orders", s.adminOrders)
	admin.POST("/orders/:id/reconcile", s.reconcileOrder)
	admin.POST("/orders/reconcile", s.reconcileOrders)
	admin.GET("/redeemcodes", s.redeemCodes)
	admin.POST("/redeemcodes", s.createRedeemCodes)
	admin.DELETE("/redeemcodes/:id", s.deleteRedeemCode)
	admin.GET("/logs", s.auditLogs)
	admin.GET("/ledger", s.ledger)
	admin.GET("/reservations", s.reservations)
	admin.POST("/reservations/:request_id/reconcile", s.reconcileReservation)
	admin.GET("/settings", s.settings)
	admin.PUT("/settings", s.saveSettings)
	admin.GET("/paymentconfig", s.paymentConfigs)
	admin.PUT("/paymentconfig/:provider", s.savePaymentConfig)
}

type channelInput struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	BaseURL    string            `json:"base_url"`
	APIKey     string            `json:"api_key"`
	ModelIDs   map[string]string `json:"model_ids"`
	Endpoints  []v1.Endpoint     `json:"endpoints"`
	Priority   int               `json:"priority"`
	Weight     int               `json:"weight"`
	Enabled    bool              `json:"enabled"`
	APIVersion string            `json:"api_version"`
}

func (in channelInput) channel(id int64) core.Channel {
	models, _ := json.Marshal(in.ModelIDs)
	endpoints, _ := json.Marshal(in.Endpoints)
	return core.Channel{ID: id, Name: in.Name, Kind: in.Kind, BaseURL: in.BaseURL, ModelsJSON: string(models), EndpointsJSON: string(endpoints), Priority: in.Priority, Weight: in.Weight, Enabled: in.Enabled, APIVersion: in.APIVersion}
}

type channelDTO struct {
	core.Channel
	ModelIDs             map[string]string `json:"model_ids"`
	Endpoints            []v1.Endpoint     `json:"endpoints"`
	CredentialConfigured bool              `json:"credential_configured"`
}

func channelView(channel core.Channel) channelDTO {
	models := map[string]string{}
	endpoints := []v1.Endpoint{}
	_ = json.Unmarshal([]byte(channel.ModelsJSON), &models)
	_ = json.Unmarshal([]byte(channel.EndpointsJSON), &endpoints)
	return channelDTO{Channel: channel, ModelIDs: models, Endpoints: endpoints, CredentialConfigured: channel.EncryptedKey != ""}
}
func (s *Server) channels(c *gin.Context) {
	items, err := s.Store.ListChannels(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	out := []channelDTO{}
	for _, item := range items {
		out = append(out, channelView(item))
	}
	c.JSON(200, gin.H{"items": out})
}
func (s *Server) createChannel(c *gin.Context) {
	var in channelInput
	if !s.decode(c, &in) {
		return
	}
	channel, err := s.Store.CreateChannel(c.Request.Context(), in.channel(0), in.APIKey)
	if err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "channel.create", strconvID(channel.ID)) {
		return
	}
	c.JSON(201, gin.H{"channel": channelView(channel)})
}
func (s *Server) updateChannel(c *gin.Context) {
	id, ok := s.id(c)
	if !ok {
		return
	}
	var in channelInput
	if !s.decode(c, &in) {
		return
	}
	err := s.Store.UpdateChannel(c.Request.Context(), in.channel(id), in.APIKey, s.checkedChannelAudit(c, "channel.update"))
	if err != nil {
		if !publicationFailure(c, err) {
			s.fail(c, err)
		}
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
func (s *Server) deleteChannel(c *gin.Context) {
	id, ok := s.id(c)
	if !ok {
		return
	}
	err := s.Store.DeleteChannel(c.Request.Context(), id, func(tx *gorm.DB, old core.Channel) error {
		updated := old
		updated.Enabled = false
		return s.checkedChannelAudit(c, "channel.delete")(tx, old, updated)
	})
	if err != nil {
		if !publicationFailure(c, err) {
			s.fail(c, err)
		}
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
func (s *Server) probeChannel(c *gin.Context) {
	id, ok := s.id(c)
	if !ok {
		return
	}
	if err := s.Relay.ProbeChannel(c.Request.Context(), id); err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "channel.probe", strconvID(id)) {
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

type modelDTO struct {
	v1.Model
	Enabled          bool `json:"enabled"`
	SubscriptionOnly bool `json:"subscription_only"`
}

func (s *Server) adminModels(c *gin.Context) {
	models, err := s.Store.ListModels(c.Request.Context(), false)
	if s.databaseError(c, err) {
		return
	}
	var records []core.Model
	if s.databaseError(c, s.Store.DB().WithContext(c.Request.Context()).Find(&records).Error) {
		return
	}
	settings := map[string]core.Model{}
	for _, record := range records {
		settings[record.ID] = record
	}
	items := []modelDTO{}
	for _, model := range models {
		record := settings[model.ID]
		items = append(items, modelDTO{Model: model, Enabled: record.Enabled, SubscriptionOnly: record.SubscriptionOnly})
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) saveModel(c *gin.Context) {
	var in struct {
		v1.Model
		Enabled          *bool `json:"enabled"`
		SubscriptionOnly *bool `json:"subscription_only"`
	}
	if !s.decode(c, &in) {
		return
	}
	create := c.Param("id") == ""
	enabled := false
	subscriptionOnly := false
	if !create {
		if in.ID != c.Param("id") {
			s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "The public model ID cannot change during editing."})
			return
		}
		existing, err := s.Store.ModelRecord(c.Request.Context(), in.ID)
		if err != nil {
			s.fail(c, err)
			return
		}
		enabled = existing.Enabled
		subscriptionOnly = existing.SubscriptionOnly
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if in.SubscriptionOnly != nil {
		subscriptionOnly = *in.SubscriptionOnly
	}
	model := modelDTO{Model: in.Model, Enabled: enabled, SubscriptionOnly: subscriptionOnly}
	model.IncludedInPlan = false
	var err error
	status := 200
	if create {
		status = 201
		err = s.Store.CreateModelAccess(c.Request.Context(), in.Model, enabled, subscriptionOnly, s.checkedModelAudit(c, model, "model.create"))
	} else {
		err = s.Store.UpdateModelAccess(c.Request.Context(), in.Model, enabled, subscriptionOnly, s.checkedModelAudit(c, model, "model.update"))
	}
	if err != nil {
		if !publicationFailure(c, err) {
			s.fail(c, err)
		}
		return
	}
	c.JSON(status, gin.H{"model": model})
}
func (s *Server) disableModel(c *gin.Context) {
	id := c.Param("id")
	result := s.Store.DB().WithContext(c.Request.Context()).Model(&core.Model{}).Where("id = ?", id).Update("enabled", false)
	if result.Error != nil {
		s.databaseError(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		s.fail(c, store.ErrNotFound)
		return
	}
	if !s.audit(c, "model.disable", id) {
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
func (s *Server) savePricing(c *gin.Context) {
	var in struct {
		Pricing []v1.Price `json:"pricing"`
	}
	if !s.decode(c, &in) {
		return
	}
	id := c.Param("id")
	err := s.Store.UpdateModelPricing(c.Request.Context(), id, in.Pricing, func(tx *gorm.DB, model store.OnboardingModel) error {
		dto := modelDTO{Model: model.Definition, Enabled: model.Enabled, SubscriptionOnly: model.SubscriptionOnly}
		return s.checkedModelAudit(c, dto, "pricing.save")(tx)
	})
	if err != nil {
		if !publicationFailure(c, err) {
			s.fail(c, err)
		}
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) adminPlans(c *gin.Context) {
	plans, err := s.Store.ListPlans(c.Request.Context(), false)
	if s.databaseError(c, err) {
		return
	}
	items := []planDTO{}
	for _, plan := range plans {
		items = append(items, planView(plan))
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) savePlan(c *gin.Context) {
	var in planInput
	if !s.decode(c, &in) {
		return
	}
	id := int64(0)
	if c.Param("id") != "" {
		var ok bool
		id, ok = s.id(c)
		if !ok {
			return
		}
	}
	plan, err := s.Store.SavePlan(c.Request.Context(), in.plan(id))
	if err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "plan.save", strconvID(plan.ID)) {
		return
	}
	c.JSON(200, gin.H{"plan": planView(plan)})
}
func (s *Server) users(c *gin.Context) {
	items, err := s.Store.ListUsers(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) updateUser(c *gin.Context) {
	id, ok := s.id(c)
	if !ok {
		return
	}
	var in struct {
		Disabled *bool `json:"disabled"`
		Admin    *bool `json:"admin"`
	}
	if !s.decode(c, &in) {
		return
	}
	if in.Disabled == nil && in.Admin == nil {
		s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "Provide at least one account access control."})
		return
	}
	if err := s.Store.UpdateUserAdmin(c.Request.Context(), user(c).ID, id, in.Disabled, in.Admin); err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "user.access", strconvID(id)) {
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
func (s *Server) credit(c *gin.Context) {
	id, ok := s.id(c)
	if !ok {
		return
	}
	var in struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Reason   string `json:"reason"`
	}
	if !s.decode(c, &in) {
		return
	}
	if in.Amount <= 0 || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 500 {
		s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "A positive integer amount and audit reason are required."})
		return
	}
	var entry core.LedgerEntry
	err := s.Store.Transaction(c.Request.Context(), func(tx *gorm.DB) error {
		var actor core.User
		if err := tx.Where("id = ? AND admin = ? AND disabled = ?", user(c).ID, true, false).First(&actor).Error; err != nil {
			return store.ErrUnauthorized
		}
		var err error
		entry, err = s.Billing.CreditTx(tx, id, "admin:"+c.Writer.Header().Get("x-request-id"), in.Amount, in.Currency)
		if err != nil {
			return err
		}
		return tx.Create(&core.AuditLog{UserID: actor.ID, Action: "wallet.credit: " + in.Reason, Resource: strconvID(id)}).Error
	})
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(200, gin.H{"entry": entry})
}
func (s *Server) adminOrders(c *gin.Context) {
	items, err := s.Payment.ListOrders(c.Request.Context(), 0, 100)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) reconcileOrder(c *gin.Context) {
	order, err := s.Payment.QueryReconcile(c.Request.Context(), c.Param("id"))
	if err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "payment.reconcile", order.ID) {
		return
	}
	c.JSON(200, gin.H{"order": order})
}
func (s *Server) reconcileOrders(c *gin.Context) {
	items, err := s.Payment.Reconcile(c.Request.Context(), 100)
	if err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "payment.reconcile_batch", "pending") {
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) redeemCodes(c *gin.Context) {
	items, err := s.Store.ListRedeemCodes(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) createRedeemCodes(c *gin.Context) {
	var in struct {
		Amount    int64      `json:"amount"`
		Currency  string     `json:"currency"`
		PlanID    *int64     `json:"plan_id"`
		Count     int        `json:"count"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if !s.decode(c, &in) {
		return
	}
	if in.Count < 1 || in.Count > 100 {
		s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "Code count must be between 1 and 100."})
		return
	}
	_, codes, err := s.Store.CreateRedeemCodes(c.Request.Context(), user(c).ID, core.RedeemCode{Amount: in.Amount, Currency: in.Currency, PlanID: in.PlanID, ExpiresAt: in.ExpiresAt}, in.Count)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(201, gin.H{"codes": codes})
}
func (s *Server) deleteRedeemCode(c *gin.Context) {
	id, ok := s.id(c)
	if !ok {
		return
	}
	if err := s.Store.DeleteRedeemCode(c.Request.Context(), id); err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "redeem_code.delete", strconvID(id)) {
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
func (s *Server) auditLogs(c *gin.Context) {
	items, err := s.Store.ListAudit(c.Request.Context(), 100)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) ledger(c *gin.Context) {
	items, err := s.Store.ListLedger(c.Request.Context(), 0, 100)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) reservations(c *gin.Context) {
	items, err := s.Store.ListUsage(c.Request.Context(), 0, 100)
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) reconcileReservation(c *gin.Context) {
	var in struct {
		Action string     `json:"action"`
		Reason string     `json:"reason"`
		Usage  core.Usage `json:"usage"`
	}
	if !s.decode(c, &in) {
		return
	}
	if in.Action != "settle" && in.Action != "release" {
		s.fail(c, &relay.Error{Status: 400, Code: "invalid_request_error", Message: "Reconciliation action must be settle or release."})
		return
	}
	reservation, err := s.Billing.Reconcile(c.Request.Context(), c.Param("request_id"), in.Action, in.Reason, user(c).ID, in.Usage)
	if err != nil {
		s.fail(c, err)
		return
	}
	c.JSON(200, gin.H{"reservation": reservation})
}
func (s *Server) settings(c *gin.Context) {
	settings, err := s.instance(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, settings)
}
func (s *Server) saveSettings(c *gin.Context) {
	var in InstanceSettings
	if !s.decode(c, &in) {
		return
	}
	if in.Currency == "" {
		current, err := s.instance(c.Request.Context())
		if s.databaseError(c, err) {
			return
		}
		in.Currency = current.Currency
	}
	if err := validateSettings(in); err != nil {
		s.fail(c, err)
		return
	}
	b, err := json.Marshal(in)
	if s.databaseError(c, err) {
		return
	}
	if s.databaseError(c, s.Store.SetSetting(c.Request.Context(), "instance", b)) {
		return
	}
	if !s.audit(c, "settings.save", "instance") {
		return
	}
	c.JSON(200, in)
}
func (s *Server) paymentConfigs(c *gin.Context) {
	items, err := s.PaymentConfig.Status(c.Request.Context())
	if s.databaseError(c, err) {
		return
	}
	c.JSON(200, gin.H{"items": items})
}
func (s *Server) savePaymentConfig(c *gin.Context) {
	var in payment.ConfigPatch
	if !s.decode(c, &in) {
		return
	}
	status, err := s.PaymentConfig.Update(c.Request.Context(), c.Param("provider"), in)
	if err != nil {
		s.fail(c, err)
		return
	}
	if !s.audit(c, "payment_config.save", c.Param("provider")) {
		return
	}
	c.JSON(200, gin.H{"config": status})
}
