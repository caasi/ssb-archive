package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreKeepsUsedExportsAcrossRestart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "consent.json")
	st, err := openStore(p)
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(time.Hour)
	if ok, err := st.claimExport("@a sig", until); !ok || err != nil {
		t.Fatalf("first claim = %v %v", ok, err)
	}
	again, _ := openStore(p)
	if ok, _ := again.claimExport("@a sig", until); ok {
		t.Fatal("a used export proof worked again after a restart")
	}
	if ok, _ := again.claimExport("@a other", until); !ok {
		t.Fatal("another proof was refused")
	}
}

func TestStoreDropsExpiredExports(t *testing.T) {
	p := filepath.Join(t.TempDir(), "consent.json")
	st, _ := openStore(p)
	st.claimExport("@a old", time.Now().Add(-time.Minute))
	st.claimExport("@a new", time.Now().Add(time.Hour))
	again, _ := openStore(p)
	if _, ok := again.used["@a old"]; ok {
		t.Fatal("expired entry kept")
	}
}

func TestStoreReadsTheOldFormat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "consent.json")
	os.WriteFile(p, []byte(`{"@a": {"action": "hide", "time": "2026-10-26T08:29:00Z"}}`), 0o600)
	st, err := openStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := st.get("@a"); !ok || d.Action != "hide" {
		t.Fatalf("old decision lost: %+v", d)
	}
}
