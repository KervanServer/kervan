package auth

import "testing"

func TestPasswordRoundTripArgon2ID(t *testing.T) {
	hash, err := HashPassword("s3cret!", "argon2id")
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	if !VerifyPassword("s3cret!", hash) {
		t.Fatal("verify failed")
	}
	if VerifyPassword("wrong", hash) {
		t.Fatal("verify should fail for wrong password")
	}
}

func TestPasswordRoundTripBcrypt(t *testing.T) {
	hash, err := HashPassword("s3cret!", "bcrypt")
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	if !VerifyPassword("s3cret!", hash) {
		t.Fatal("verify failed")
	}
}

func TestValidatePasswordHash(t *testing.T) {
	argonHash, err := HashPassword("s3cret!", "argon2id")
	if err != nil {
		t.Fatalf("argon hash error: %v", err)
	}
	if err := ValidatePasswordHash(argonHash); err != nil {
		t.Fatalf("expected argon hash to validate, got %v", err)
	}

	bcryptHash, err := HashPassword("s3cret!", "bcrypt")
	if err != nil {
		t.Fatalf("bcrypt hash error: %v", err)
	}
	if err := ValidatePasswordHash(bcryptHash); err != nil {
		t.Fatalf("expected bcrypt hash to validate, got %v", err)
	}

	if err := ValidatePasswordHash("not-a-hash"); err == nil {
		t.Fatal("expected invalid hash to be rejected")
	}
}
