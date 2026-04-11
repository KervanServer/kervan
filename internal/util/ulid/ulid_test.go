package ulid

import (
	"strings"
	"testing"
)

func TestNewReturnsValidMonotonicULIDLikeValue(t *testing.T) {
	last = 0

	first := New()
	second := New()

	if len(first) != 26 || len(second) != 26 {
		t.Fatalf("expected 26-char identifiers, got %q and %q", first, second)
	}
	for _, id := range []string{first, second} {
		for _, ch := range id {
			if !strings.ContainsRune(crockford, ch) {
				t.Fatalf("identifier contains non-crockford rune %q in %q", ch, id)
			}
		}
	}
	if second <= first {
		t.Fatalf("expected monotonic identifiers, got first=%q second=%q", first, second)
	}
}

func TestEncodeDeterministicLength(t *testing.T) {
	raw := [16]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	encoded := encode(raw)

	if len(encoded) != 26 {
		t.Fatalf("expected encoded length 26, got %d", len(encoded))
	}
	if encoded != encode(raw) {
		t.Fatalf("expected deterministic encoding, got %q", encoded)
	}
}
