package account

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRoundRobinAndCooldown(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPool(store, 0.1, 1)
	now := time.Now().UTC()
	a1 := &Account{ID: "a1", AccessToken: "t1", Credits: 10, CreatedAt: now, UpdatedAt: now}
	a2 := &Account{ID: "a2", AccessToken: "t2", Credits: 10, CreatedAt: now, UpdatedAt: now}
	if err := p.Save(a1); err != nil {
		t.Fatal(err)
	}
	if err := p.Save(a2); err != nil {
		t.Fatal(err)
	}

	s1, err := p.Select()
	if err != nil {
		t.Fatal(err)
	}
	s2, err := p.Select()
	if err != nil {
		t.Fatal(err)
	}
	if s1.ID == s2.ID {
		t.Fatalf("expected different accounts, got %s twice", s1.ID)
	}
	// both in flight at concurrency 1 each; third should fail
	if _, err := p.Select(); err == nil {
		t.Fatal("expected no available account")
	}
	p.Release(s1.ID)
	p.Release(s2.ID)

	p.MarkError("a1", "not_enough_credits", time.Hour, false)
	s3, err := p.Select()
	if err != nil {
		t.Fatal(err)
	}
	if s3.ID != "a2" {
		t.Fatalf("expected a2 after a1 cooldown, got %s", s3.ID)
	}
	p.Release(s3.ID)
}

func TestImportPathExists(t *testing.T) {
	// smoke: store path creation
	dir := filepath.Join(t.TempDir(), "data")
	_, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "accounts")); err != nil {
		t.Fatal(err)
	}
}
