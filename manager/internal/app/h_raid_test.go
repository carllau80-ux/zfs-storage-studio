package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRaidCapabilitiesAndWriteGate(t *testing.T) {
	a := &App{}

	ro := &NodeRow{Name: "prod", CapJSON: `{"raid":{"available":true,"allow_mutation":false}}`}
	avail, allow := a.raidCap(ro)
	if !avail || allow {
		t.Fatalf("raidCap = %v,%v; want true,false", avail, allow)
	}
	rec := httptest.NewRecorder()
	if a.raidWriteGate(rec, ro) {
		t.Fatal("read-only node must be rejected")
	}
	if rec.Code != 403 {
		t.Fatalf("status = %d; want 403", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	e, _ := body["error"].(map[string]any)
	if e["code"] != "RAID_READ_ONLY" {
		t.Fatalf("code = %v; want RAID_READ_ONLY", e["code"])
	}

	rw := &NodeRow{CapJSON: `{"raid":{"available":true,"allow_mutation":true}}`}
	rec = httptest.NewRecorder()
	if !a.raidWriteGate(rec, rw) || rec.Code != 200 {
		t.Fatalf("writable node rejected: %d", rec.Code)
	}

	none := &NodeRow{CapJSON: `{}`}
	avail, allow = a.raidCap(none)
	if avail || allow {
		t.Fatalf("node without raid capability should be unavailable")
	}
}

func TestRaidReadOnlyMessageMentionsNode(t *testing.T) {
	a := &App{}
	n := &NodeRow{Name: "prod2128", CapJSON: `{"raid":{"available":true,"allow_mutation":false}}`}
	rec := httptest.NewRecorder()
	a.raidWriteGate(rec, n)
	if !strings.Contains(rec.Body.String(), "prod2128") ||
		!strings.Contains(rec.Body.String(), "只读模式") {
		t.Fatalf("gate message lacks node/read-only hint: %s", rec.Body.String())
	}
}
