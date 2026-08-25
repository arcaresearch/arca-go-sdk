package arca

import (
	"encoding/json"
	"testing"
)

// The wire strings are the contract with the server; a typo here silently means
// the client never sees the event at all, which is indistinguishable from the
// account never becoming ready.
func TestExchangeProvisioningEventWireStrings(t *testing.T) {
	if EventExchangeProvisioned != "exchange.provisioned" {
		t.Errorf("EventExchangeProvisioned = %q, want exchange.provisioned", EventExchangeProvisioned)
	}
	if EventExchangeReady != "exchange.ready" {
		t.Errorf("EventExchangeReady = %q, want exchange.ready", EventExchangeReady)
	}
}

// A provisioned event on a cosign-armed boundary is the case the two-event
// split exists for: the account is usable for reads but cannot trade until the
// user co-signs, so cosignRequired and tradable must survive the round trip
// independently.
func TestRealmEventDecodesExchangeProvisioning(t *testing.T) {
	raw := []byte(`{
		"type": "exchange.provisioned",
		"entityId": "obj_1",
		"entityPath": "/users/alice/exchange",
		"exchange": {
			"objectId": "obj_1",
			"path": "/users/alice/exchange",
			"cosignRequired": true,
			"tradable": false,
			"accountAddress": "0x8f2a",
			"agentWalletId": "wlt_1"
		}
	}`)

	var ev RealmEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev.Exchange == nil {
		t.Fatal("Exchange payload missing — the event would arrive with nothing actionable on it")
	}
	if !ev.Exchange.CosignRequired {
		t.Error("CosignRequired = false, want true")
	}
	if ev.Exchange.Tradable {
		t.Error("Tradable = true, want false — provisioned on an armed boundary cannot trade yet")
	}
	if ev.Exchange.AccountAddress != "0x8f2a" {
		t.Errorf("AccountAddress = %q, want 0x8f2a", ev.Exchange.AccountAddress)
	}
}
