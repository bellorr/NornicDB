package server

import (
	"testing"
	"time"

	"github.com/orneryd/nornicdb/pkg/auth"
)

func TestBasicAuthCache_GetSet(t *testing.T) {
	cache := newBasicAuthCache(4, 50*time.Millisecond)
	key := basicAuthCacheKey("Basic abc")
	claims := &auth.JWTClaims{Sub: "u1", Roles: []string{"admin"}}

	cache.set(key, claims)
	got, ok := cache.get(key)
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if got.Sub != "u1" {
		t.Fatalf("got sub %q, want %q", got.Sub, "u1")
	}

	// Ensure a copy is returned.
	got.Roles[0] = "mutated"
	got2, ok := cache.get(key)
	if !ok {
		t.Fatalf("expected cache hit")
	}
	if got2.Roles[0] != "admin" {
		t.Fatalf("cache entry was mutated")
	}
}

func TestBasicAuthCache_Expires(t *testing.T) {
	cache := newBasicAuthCache(4, 20*time.Millisecond)
	key := basicAuthCacheKey("Basic abc")
	cache.set(key, &auth.JWTClaims{Sub: "u1"})

	time.Sleep(30 * time.Millisecond)
	if _, ok := cache.get(key); ok {
		t.Fatalf("expected cache miss after expiry")
	}
}

