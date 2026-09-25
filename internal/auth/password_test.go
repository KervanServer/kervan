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

func TestValidatePasswordHashBoundsArgon2Params(t *testing.T) {
	cases := []string{
		"$argon2id$v=19$m=4294967295,t=1,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY",
		"$argon2id$v=19$m=8,t=999999999,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY",
		"$argon2id$v=19$m=1,t=1,p=1$MDEyMzQ1Njc4OWFiY2RlZg$MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY",
	}
	for _, hash := range cases {
		if err := ValidatePasswordHash(hash); err == nil {
			t.Fatalf("expected out-of-range argon2id parameters to be rejected: %s", hash)
		}
		if VerifyPassword("guess", hash) {
			t.Fatalf("expected out-of-range argon2id hash to fail verification: %s", hash)
		}
	}
}
