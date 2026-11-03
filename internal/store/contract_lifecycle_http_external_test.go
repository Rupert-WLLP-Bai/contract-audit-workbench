// File metadata (manifest mirror):
// Purpose: Reads nonempty lifecycle delivery and acceptance facts through the real Core HTTP service, gateway credential and original session to verify their contract correlation.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"contractaudit/internal/httpapi"
	"contractaudit/internal/store"
)

func TestContractLifecycleOverviewHTTPNonemptyFacts(t *testing.T) {
	fixture := store.NewLifecycleHTTPFixture(t)
	handler, err := httpapi.ServiceHandler(fixture.Core(), httpapi.Options{Durable: true}, "core", strings.Repeat("g", 40))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/contracts/"+fixture.ContractID()+"/lifecycle-overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "session", Value: fixture.SessionToken()})
	request.Header.Set("X-Contract-Service-Key", strings.Repeat("g", 40))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("overview status %d", response.StatusCode)
	}
	if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("missing no-store: %q", response.Header.Get("Cache-Control"))
	}
	var wire struct {
		Contract struct {
			ContractID string `json:"contract_id"`
		} `json:"contract"`
		Deliveries struct {
			Items []struct {
				ContractID string `json:"contract_id"`
				DeliveryID string `json:"delivery_id"`
			} `json:"items"`
		} `json:"deliveries"`
		Acceptances struct {
			Items []struct {
				ContractID string `json:"contract_id"`
				DeliveryID string `json:"delivery_id"`
			} `json:"items"`
		} `json:"acceptances"`
	}
	if err := json.NewDecoder(response.Body).Decode(&wire); err != nil {
		t.Fatal(err)
	}
	if wire.Contract.ContractID != fixture.ContractID() || len(wire.Deliveries.Items) != 1 || len(wire.Acceptances.Items) != 1 {
		t.Fatalf("unexpected nonempty overview: %+v", wire)
	}
	delivery, acceptance := wire.Deliveries.Items[0], wire.Acceptances.Items[0]
	if delivery.ContractID != fixture.ContractID() || acceptance.ContractID != fixture.ContractID() || delivery.DeliveryID == "" || acceptance.DeliveryID != delivery.DeliveryID {
		t.Fatalf("facts are not bound to requested contract and delivery: %+v", wire)
	}
}
