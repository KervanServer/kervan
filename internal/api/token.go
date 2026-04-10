package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type tokenClaims struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
}

func signToken(secret []byte, subject string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	claims := tokenClaims{
		Sub: subject,
		Exp: time.Now().Add(ttl).Unix(),
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	h := base64.RawURLEncoding.EncodeToString(hb)
	c := base64.RawURLEncoding.EncodeToString(cb)
	unsigned := h + "." + c

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(unsigned))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return unsigned + "." + sig, nil
}

func verifyToken(secret []byte, token string) (*tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}
	unsigned := parts[0] + "." + parts[1]

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(unsigned))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("invalid token signature")
	}
	if !hmac.Equal(got, expected) {
		return nil, errors.New("signature mismatch")
	}

	rawClaims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid claims encoding")
	}
	var claims tokenClaims
	if err := json.Unmarshal(rawClaims, &claims); err != nil {
		return nil, errors.New("invalid claims")
	}
	if claims.Exp < time.Now().Unix() {
		return nil, errors.New("token expired")
	}
	if claims.Sub == "" {
		return nil, errors.New("token subject missing")
	}
	return &claims, nil
}
