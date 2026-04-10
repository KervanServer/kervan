package vfs

import "testing"

func TestResolverResolve(t *testing.T) {
	r := NewResolver()
	got, err := r.Resolve("../etc/passwd")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "/etc/passwd" {
		t.Fatalf("unexpected cleaned path: %s", got)
	}
}

func TestResolverNullByte(t *testing.T) {
	r := NewResolver()
	_, err := r.Resolve("/tmp/a\x00b")
	if err == nil {
		t.Fatal("expected forbidden character error")
	}
}
