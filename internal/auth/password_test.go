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
