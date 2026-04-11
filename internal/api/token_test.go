package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSignAndVerifyTokenLifecycle(t *testing.T) {
	secret := []byte("super-secret")

	token, err := signToken(secret, "alice", 0)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	claims, err := verifyToken(secret, token)
	if err != nil {
		t.Fatalf("verify token: %v", err)
	}
	if claims.Sub != "alice" {
		t.Fatalf("expected subject alice, got %q", claims.Sub)
	}
	if claims.Exp <= time.Now().Unix() {
		t.Fatalf("expected future expiration, got %d", claims.Exp)
	}
}

func TestVerifyTokenRejectsInvalidForms(t *testing.T) {
	secret := []byte("super-secret")
	invalidCases := []struct {
		name  string
		token string
		want  string
	}{
		{
			name:  "bad-format",
			token: "not-a-jwt",
			want:  "invalid token format",
		},
		{
			name:  "bad-signature-encoding",
			token: "a.b.***",
			want:  "invalid token signature",
		},
		{
			name:  "bad-claims-encoding",
			token: signedToken(secret, `{"alg":"HS256","typ":"JWT"}`, "*"),
			want:  "invalid claims encoding",
		},
		{
			name:  "bad-claims-json",
			token: signedToken(secret, `{"alg":"HS256","typ":"JWT"}`, base64.RawURLEncoding.EncodeToString([]byte("not-json"))),
			want:  "invalid claims",
		},
		{
			name:  "expired",
			token: signedClaimsToken(secret, tokenClaims{Sub: "alice", Exp: time.Now().Add(-time.Minute).Unix()}),
			want:  "token expired",
		},
		{
			name:  "missing-subject",
			token: signedClaimsToken(secret, tokenClaims{Sub: "", Exp: time.Now().Add(time.Minute).Unix()}),
			want:  "token subject missing",
		},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := verifyToken(secret, tc.token)
			if err == nil {
				t.Fatalf("expected error, got claims=%#v", claims)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestVerifyTokenRejectsSignatureMismatch(t *testing.T) {
	secret := []byte("super-secret")

	token, err := signToken(secret, "alice", time.Hour)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected token format: %q", token)
	}
	parts[2] = base64.RawURLEncoding.EncodeToString([]byte("wrong-signature"))

	if _, err := verifyToken(secret, strings.Join(parts, ".")); err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Fatalf("expected signature mismatch, got %v", err)
	}
}

func signedClaimsToken(secret []byte, claims tokenClaims) string {
	header := `{"alg":"HS256","typ":"JWT"}`
	rawClaims, _ := json.Marshal(claims)
	return signedToken(secret, header, base64.RawURLEncoding.EncodeToString(rawClaims))
}

func signedToken(secret []byte, headerJSON, encodedClaims string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(headerJSON))
	unsigned := header + "." + encodedClaims
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
