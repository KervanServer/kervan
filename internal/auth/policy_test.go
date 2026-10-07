package auth

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestPasswordPolicySpecialChar(t *testing.T) {
	e := NewEngine(nil, "bcrypt", 1, time.Minute)
	e.SetMinPasswordLength(8)
	e.SetRequireSpecialChar(true)
	for pw, ok := range map[string]bool{
		"short!":         false,
		"longenough123":  false,
		"longenough123!": true,
		"pass word_1":    true,
		"çokgüzelşifre.": true,
	} {
		if err := e.ValidatePassword(pw); (err == nil) != ok {
			t.Errorf("ValidatePassword(%q) err=%v, want ok=%v", pw, err, ok)
		}
	}
	e.SetRequireSpecialChar(false)
	if err := e.ValidatePassword("longenough123"); err != nil {
		t.Errorf("special char no longer required: %v", err)
	}
}

func TestAuthorizedKeyMatchesWithCommentAndOptions(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	for _, entry := range []string{
		line,
		line + " alice@laptop",
		`no-pty,from="10.0.0.0/8" ` + line + " deploy key",
		"  " + line + "  \n",
	} {
		if !authorizedKeyMatches(entry, signer.PublicKey()) {
			t.Errorf("entry %q did not match", entry)
		}
	}
	_, other, _ := ed25519.GenerateKey(nil)
	otherSigner, _ := ssh.NewSignerFromKey(other)
	if authorizedKeyMatches(line, otherSigner.PublicKey()) || authorizedKeyMatches("garbage", signer.PublicKey()) {
		t.Error("mismatched key accepted")
	}
}
