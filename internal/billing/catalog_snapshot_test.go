// SPDX-License-Identifier: Apache-2.0
package billing

import (
	"context"
	"testing"

	v1 "github.com/nomifun/nomifun-model-gateway/contract/v1"
	"github.com/nomifun/nomifun-model-gateway/internal/core"
)

func TestCatalogRepricingDoesNotChangeAcceptedReservationSnapshot(t *testing.T) {
	s, st, user, key := fixture(t, 1000)
	ctx := context.Background()
	m := tokenModel()
	m.DisplayName, m.Vendor, m.Status = "Synthetic model", "Synthetic supplier", "available"
	m.InputModalities, m.Traits = []string{"text"}, []string{}
	m.TaskEndpoints = map[v1.Task]v1.TaskEndpoints{v1.Chat: {Endpoints: []v1.Endpoint{v1.OpenAI}, PreferredEndpoint: v1.OpenAI}}
	m.Pricing[0].Amount = 3
	if err := st.SaveModelAccess(ctx, m, true, false); err != nil {
		t.Fatal(err)
	}
	oldRequest := request(user, key, "before-repricing", 10)
	oldRequest.Model = m
	hold, err := s.Reserve(ctx, oldRequest)
	if err != nil || hold.ReservedAmount != 30 {
		t.Fatalf("old reservation = %+v, %v", hold, err)
	}
	if err := s.MarkAccepted(ctx, hold.RequestID); err != nil {
		t.Fatal(err)
	}
	oldSnapshot := hold.PricingJSON

	// The administrator edits the durable public catalog while native work is
	// in flight. Settlement must keep the accepted price even after reopening.
	m.Pricing[0].Amount = 17
	if err := st.SaveModelAccess(ctx, m, true, false); err != nil {
		t.Fatal(err)
	}
	current, err := st.Model(ctx, m.ID)
	if err != nil || current.Pricing[0].Amount != 17 {
		t.Fatalf("current catalog = %+v, %v", current, err)
	}
	reopened := New(st.DB())
	reopened.Now = s.Now
	changedReplay := oldRequest
	changedReplay.Model = current
	_, err = reopened.Reserve(ctx, changedReplay)
	requireCode(t, err, "idempotency_conflict")
	settled, err := reopened.Settle(ctx, hold.RequestID, finalUsage(7))
	if err != nil || settled.ActualAmount != 21 || settled.PricingJSON != oldSnapshot {
		t.Fatalf("snapshot settlement = %+v, %v", settled, err)
	}
	if _, err := reopened.Settle(ctx, hold.RequestID, finalUsage(7)); err != nil {
		t.Fatal(err)
	}

	newRequest := request(user, key, "after-repricing", 10)
	newRequest.Model = current
	newHold, err := reopened.Reserve(ctx, newRequest)
	if err != nil || newHold.ReservedAmount != 170 || newHold.PricingJSON == oldSnapshot {
		t.Fatalf("new reservation = %+v, %v", newHold, err)
	}
	newSettlement, err := reopened.Settle(ctx, newHold.RequestID, finalUsage(7))
	if err != nil || newSettlement.ActualAmount != 119 {
		t.Fatalf("new settlement = %+v, %v", newSettlement, err)
	}
	u, k := balances(t, st.DB(), user.ID, key.ID)
	if u.Balance != 860 || u.ReservedBalance != 0 || k.QuotaUsed != 14 || k.QuotaReserved != 0 {
		t.Fatalf("accounting after repricing = %+v, %+v", u, k)
	}
	var entries []core.LedgerEntry
	if err := st.DB().Order("id").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Amount != -21 || entries[1].Amount != -119 {
		t.Fatalf("idempotent ledger = %+v", entries)
	}
}
