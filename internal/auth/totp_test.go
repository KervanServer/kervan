package auth

import (
	"testing"
	"time"
)

func TestGenerateAndValidateTOTP(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	at := time.Unix(1712332800, 0).UTC()

	code, err := GenerateTOTPCode(secret, at)
	if err != nil {
		t.Fatalf("GenerateTOTPCode: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("expected 6-digit code, got %q", code)
	}
	if !ValidateTOTP(secret, code, at, 1) {
		t.Fatal("expected code to validate")
	}
	if ValidateTOTP(secret, "000000", at, 1) {
		t.Fatal("expected invalid code to fail")
	}
}

func TestTOTPProvisioningURL(t *testing.T) {
	url := TOTPProvisioningURL("Kervan", "alice", "JBSWY3DPEHPK3PXP")
	if url == "" {
		t.Fatal("expected provisioning url")
	}
	if want := "otpauth://totp/"; url[:len(want)] != want {
		t.Fatalf("expected otpauth url, got %q", url)
	}
}

func TestGenerateTOTPCodeRejectsPreEpochTime(t *testing.T) {
	if _, err := GenerateTOTPCode("JBSWY3DPEHPK3PXP", time.Unix(-1, 0).UTC()); err == nil {
		t.Fatal("expected pre-epoch timestamp to be rejected")
	}
}
