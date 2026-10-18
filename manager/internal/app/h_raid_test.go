package app

import "testing"

func TestRaidCapabilities(t *testing.T) {
	a := &App{}

	ro := &NodeRow{Name: "prod", CapJSON: `{"raid":{"available":true,"allow_mutation":false}}`}
	avail, allow := a.raidCap(ro)
	if !avail || allow {
		t.Fatalf("raidCap = %v,%v; want true,false", avail, allow)
	}
	if raidStaticWritable(ro) {
		t.Fatal("read-only node must not be statically writable")
	}

	rw := &NodeRow{CapJSON: `{"raid":{"available":true,"allow_mutation":true}}`}
	if !raidStaticWritable(rw) {
		t.Fatal("writable node should be statically writable")
	}

	blocked := &NodeRow{CapJSON: `{"allow_mutation":false,"raid":{"available":true,"allow_mutation":true}}`}
	if raidStaticWritable(blocked) {
		t.Fatal("node-level read-only must override raid allow")
	}

	none := &NodeRow{CapJSON: `{}`}
	avail, allow = a.raidCap(none)
	if avail || allow {
		t.Fatalf("node without raid capability should be unavailable")
	}
}

func TestRaidWriteAllowedWithTemporaryUnlock(t *testing.T) {
	ro := &NodeRow{CapJSON: `{"raid":{"available":true,"allow_mutation":false}}`}
	if raidWriteAllowed(ro, map[string]any{"raid_effective": false}) {
		t.Fatal("locked read-only node must reject writes")
	}
	if !raidWriteAllowed(ro, map[string]any{"raid_effective": true}) {
		t.Fatal("temporarily unlocked node should allow writes")
	}
	rw := &NodeRow{CapJSON: `{"raid":{"available":true,"allow_mutation":true}}`}
	if !raidWriteAllowed(rw, nil) {
		t.Fatal("statically writable node should allow writes")
	}
}
