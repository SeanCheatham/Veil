package routing

import (
	"testing"

	"github.com/veil-protocol/veil/pkg/crypto"
)

func makeRelays(n int) []RelayInfo {
	relays := make([]RelayInfo, n)
	for i := 0; i < n; i++ {
		kp, _ := crypto.GenerateKeyPair()
		relays[i] = RelayInfo{
			ID:     string(rune('A' + i)),
			Host:   "relay-" + string(rune('1'+i)),
			PubKey: kp.Public,
		}
	}
	return relays
}

func TestSelectRouteLength(t *testing.T) {
	relays := makeRelays(5)
	for i := 0; i < 50; i++ {
		route, err := SelectRoute(relays, 3, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(route) < 3 || len(route) > 5 {
			t.Fatalf("route length %d not in [3,5]", len(route))
		}
	}
}

func TestSelectRouteSubset(t *testing.T) {
	relays := makeRelays(5)
	route, err := SelectRoute(relays, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool)
	for _, r := range relays {
		ids[r.ID] = true
	}
	for _, r := range route {
		if !ids[r.ID] {
			t.Fatalf("route relay %s not in input set", r.ID)
		}
	}
}

func TestSelectRouteNoDuplicates(t *testing.T) {
	relays := makeRelays(5)
	route, err := SelectRoute(relays, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, r := range route {
		if seen[r.ID] {
			t.Fatalf("duplicate relay %s in route", r.ID)
		}
		seen[r.ID] = true
	}
}

func TestSelectRouteDifferentOrderings(t *testing.T) {
	relays := makeRelays(5)
	orders := make(map[string]bool)
	for i := 0; i < 30; i++ {
		route, err := SelectRoute(relays, 3, 3)
		if err != nil {
			t.Fatal(err)
		}
		key := ""
		for _, r := range route {
			key += r.ID
		}
		orders[key] = true
	}
	if len(orders) < 2 {
		t.Fatal("expected multiple different orderings")
	}
}

func TestSelectRouteErrors(t *testing.T) {
	relays := makeRelays(2)
	_, err := SelectRoute(relays, 3, 5)
	if err == nil {
		t.Fatal("expected error for not enough relays")
	}
	_, err = SelectRoute(relays, 0, 2)
	if err == nil {
		t.Fatal("expected error for minHops < 1")
	}
	_, err = SelectRoute(relays, 3, 1)
	if err == nil {
		t.Fatal("expected error for maxHops < minHops")
	}
}
