package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- RFC 6238 TOTP uses HMAC-SHA1 by default.
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	totpPeriod = 30
	totpDigits = 6
)

func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return strings.TrimRight(base32.StdEncoding.EncodeToString(raw), "="), nil
}

func GenerateTOTPCode(secret string, at time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	unix := at.Unix()
	if unix < 0 {
		return "", fmt.Errorf("time before unix epoch is not supported")
	}
	counter := uint64(unix / totpPeriod)
	var counterBytes [8]byte
	binary.BigEndian.PutUint64(counterBytes[:], counter)

	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counterBytes[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0F
	code := (int(sum[offset])&0x7F)<<24 |
		int(sum[offset+1])<<16 |
		int(sum[offset+2])<<8 |
		int(sum[offset+3])
	code %= 1000000
	return fmt.Sprintf("%06d", code), nil
}

func ValidateTOTP(secret, code string, at time.Time, window int) bool {
	code = normalizeTOTPCode(code)
	if len(code) != totpDigits {
		return false
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	for offset := -window; offset <= window; offset++ {
		candidate, err := GenerateTOTPCode(secret, at.Add(time.Duration(offset*totpPeriod)*time.Second))
		if err != nil {
			return false
		}
		if candidate == code {
			return true
		}
	}
	return false
}

func TOTPProvisioningURL(issuer, username, secret string) string {
	issuer = strings.TrimSpace(issuer)
	if issuer == "" {
		issuer = "Kervan"
	}
	label := issuer + ":" + strings.TrimSpace(username)
	values := url.Values{}
	values.Set("secret", strings.TrimSpace(secret))
	values.Set("issuer", issuer)
	values.Set("algorithm", "SHA1")
	values.Set("digits", strconv.Itoa(totpDigits))
	values.Set("period", strconv.Itoa(totpPeriod))
	return "otpauth://totp/" + url.PathEscape(label) + "?" + values.Encode()
}

func normalizeTOTPCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, code)
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	normalized := strings.ToUpper(strings.TrimSpace(secret))
	normalized += strings.Repeat("=", (8-len(normalized)%8)%8)
	return base32.StdEncoding.DecodeString(normalized)
}
