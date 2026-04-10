package api

import (
	"strings"
	"testing"
	"time"

	"github.com/kervanserver/kervan/internal/store"
)

func TestShareLinkRepositoryCreateGetIncrement(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	repo := newShareLinkRepository(st)
	link, err := repo.Create("alice", "/docs/report.pdf", 24*time.Hour, 3)
	if err != nil {
		t.Fatalf("create share link: %v", err)
	}
	if link == nil {
		t.Fatal("expected created link")
	}
	if !strings.HasPrefix(link.Token, "share_") {
		t.Fatalf("unexpected token: %s", link.Token)
	}
	if link.DownloadCount != 0 {
		t.Fatalf("expected initial download count 0, got %d", link.DownloadCount)
	}

	saved, err := repo.Get(link.Token)
	if err != nil {
		t.Fatalf("get share link: %v", err)
	}
	if saved.Path != "/docs/report.pdf" {
		t.Fatalf("unexpected stored path: %s", saved.Path)
	}

	if err := repo.Increment(link.Token); err != nil {
		t.Fatalf("increment download count: %v", err)
	}
	updated, err := repo.Get(link.Token)
	if err != nil {
		t.Fatalf("get share link after increment: %v", err)
	}
	if updated.DownloadCount != 1 {
		t.Fatalf("expected download count 1, got %d", updated.DownloadCount)
	}

	list, err := repo.ListByUsername("alice")
	if err != nil {
		t.Fatalf("list links: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one link for alice, got %d", len(list))
	}
	if err := repo.Delete(link.Token); err != nil {
		t.Fatalf("delete link: %v", err)
	}
	if _, err := repo.Get(link.Token); err == nil {
		t.Fatal("expected deleted link to be missing")
	}
}

func TestParseTTL(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		fallback time.Duration
		want     time.Duration
		wantErr  bool
	}{
		{name: "empty uses fallback", raw: "", fallback: 24 * time.Hour, want: 24 * time.Hour},
		{name: "hours", raw: "12h", fallback: time.Hour, want: 12 * time.Hour},
		{name: "days", raw: "2d", fallback: time.Hour, want: 48 * time.Hour},
		{name: "invalid", raw: "abc", fallback: time.Hour, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTTL(tc.raw, tc.fallback)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("unexpected ttl: got=%v want=%v", got, tc.want)
			}
		})
	}
}
