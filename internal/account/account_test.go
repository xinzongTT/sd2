package account

import (
	"testing"
	"time"
)

func TestStatusLabelExpired(t *testing.T) {
	now := time.Now()
	a := &Account{
		AccessToken:  "oat_x",
		RefreshToken: "",
		ExpiresAt:    now.Add(-time.Hour).Unix(),
	}
	if a.StatusLabel(now) != "expired" {
		t.Fatalf("got %s", a.StatusLabel(now))
	}
	if a.Healthy(0, now) {
		t.Fatal("should not be healthy without refresh")
	}
	pub := a.Public()
	if pub["ready"] != false {
		t.Fatal("ready should be false")
	}
	if pub["auth_status"] != "expired" {
		t.Fatalf("auth_status=%v", pub["auth_status"])
	}
}

func TestStatusLabelRefreshFailed(t *testing.T) {
	now := time.Now()
	a := &Account{
		AccessToken:  "oat_x",
		RefreshToken: "rt",
		ExpiresAt:    now.Add(-time.Hour).Unix(),
		AuthStatus:   "refresh_failed",
		LastError:    "refresh failed: invalid_grant",
	}
	if a.StatusLabel(now) != "refresh_failed" {
		t.Fatalf("got %s", a.StatusLabel(now))
	}
	if a.Healthy(0, now) {
		t.Fatal("should not be healthy")
	}
}

func TestStatusLabelValid(t *testing.T) {
	now := time.Now()
	a := &Account{
		AccessToken: "oat_x",
		ExpiresAt:   now.Add(2 * time.Hour).Unix(),
		Credits:     10,
		AuthStatus:  "valid",
	}
	if a.StatusLabel(now) != "valid" {
		t.Fatalf("got %s", a.StatusLabel(now))
	}
	if !a.Healthy(0.1, now) {
		t.Fatal("should be healthy")
	}
	if a.Public()["ready"] != true {
		t.Fatal("ready true")
	}
}
