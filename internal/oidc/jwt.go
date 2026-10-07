package oidc

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

type jwtHeader struct {
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}

type jwt struct {
	header       jwtHeader
	claims       Claims
	signingInput []byte
	signature    []byte
}

const maxJWTBytes = 64 << 10

func parseJWT(raw string) (*jwt, error) {
	if len(raw) > maxJWTBytes {
		return nil, errors.New("oidc: token too large")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("oidc: malformed token")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("oidc: malformed token header")
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("oidc: malformed token payload")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("oidc: malformed token signature")
	}
	var t jwt
	if err := json.Unmarshal(headerJSON, &t.header); err != nil {
		return nil, errors.New("oidc: malformed token header")
	}
	if err := json.Unmarshal(payloadJSON, &t.claims); err != nil {
		return nil, errors.New("oidc: malformed token payload")
	}
	t.signingInput = []byte(parts[0] + "." + parts[1])
	t.signature = sig
	return &t, nil
}

// jwk is one JSON Web Key (RFC 7517) of the kinds providers use for ID
// tokens: RSA, EC (P-256/384/521) and OKP (Ed25519).
type jwk struct {
	KeyType   string `json:"kty"`
	KeyID     string `json:"kid"`
	Use       string `json:"use"`
	Algorithm string `json:"alg"`
	N         string `json:"n"`
	E         string `json:"e"`
	Curve     string `json:"crv"`
	X         string `json:"x"`
	Y         string `json:"y"`
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

type publicKey struct {
	id  string
	alg string
	key crypto.PublicKey
}

type keySet struct {
	keys []publicKey
}

func (s *keySet) has(kid string) bool {
	for _, k := range s.keys {
		if k.id == kid {
			return true
		}
	}
	return false
}

func b64int(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil, errors.New("invalid key parameter")
	}
	return new(big.Int).SetBytes(b), nil
}

func parseJWKS(raw jwks) (*keySet, error) {
	set := &keySet{}
	for _, k := range raw.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		var key crypto.PublicKey
		switch k.KeyType {
		case "RSA":
			n, err := b64int(k.N)
			if err != nil {
				continue
			}
			e, err := b64int(k.E)
			if err != nil || !e.IsInt64() || e.Int64() < 3 || e.Int64() > 1<<31 {
				continue
			}
			if n.BitLen() < 2048 {
				continue // refuse weak keys
			}
			key = &rsa.PublicKey{N: n, E: int(e.Int64())}
		case "EC":
			var curve elliptic.Curve
			switch k.Curve {
			case "P-256":
				curve = elliptic.P256()
			case "P-384":
				curve = elliptic.P384()
			case "P-521":
				curve = elliptic.P521()
			default:
				continue
			}
			x, errX := base64.RawURLEncoding.DecodeString(k.X)
			y, errY := base64.RawURLEncoding.DecodeString(k.Y)
			size := (curve.Params().BitSize + 7) / 8
			if errX != nil || errY != nil || len(x) > size || len(y) > size {
				continue
			}
			// Uncompressed SEC 1 point; parsing rejects points off the curve.
			point := make([]byte, 1+2*size)
			point[0] = 4
			copy(point[1+size-len(x):1+size], x)
			copy(point[1+2*size-len(y):], y)
			pub, err := ecdsa.ParseUncompressedPublicKey(curve, point)
			if err != nil {
				continue
			}
			key = pub
		case "OKP":
			if k.Curve != "Ed25519" {
				continue
			}
			x, err := base64.RawURLEncoding.DecodeString(k.X)
			if err != nil || len(x) != ed25519.PublicKeySize {
				continue
			}
			key = ed25519.PublicKey(x)
		default:
			continue
		}
		set.keys = append(set.keys, publicKey{id: k.KeyID, alg: k.Algorithm, key: key})
	}
	if len(set.keys) == 0 {
		return nil, errors.New("oidc jwks: no usable signing keys")
	}
	return set, nil
}

func hashFor(alg string) (crypto.Hash, error) {
	switch alg[2:] {
	case "256":
		return crypto.SHA256, nil
	case "384":
		return crypto.SHA384, nil
	case "512":
		return crypto.SHA512, nil
	default:
		return 0, fmt.Errorf("oidc: unsupported algorithm %q", alg)
	}
}

// verify checks t's signature with the key named by its kid (or, without a
// kid, any key compatible with its algorithm). "none" and HMAC algorithms
// are always rejected: an ID token must be signed with the provider's
// published asymmetric keys.
func (s *keySet) verify(t *jwt) error {
	alg := t.header.Algorithm
	if len(alg) != 5 || !(strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "PS") || strings.HasPrefix(alg, "ES")) {
		if alg != "EdDSA" {
			return fmt.Errorf("oidc: unsupported or insecure algorithm %q", alg)
		}
	}
	tried := false
	for _, k := range s.keys {
		if t.header.KeyID != "" && k.id != t.header.KeyID {
			continue
		}
		if k.alg != "" && k.alg != alg {
			continue
		}
		ok, applicable := verifyWith(k.key, alg, t.signingInput, t.signature)
		if !applicable {
			continue
		}
		tried = true
		if ok {
			return nil
		}
	}
	if !tried {
		return errors.New("oidc: no matching key for token")
	}
	return errors.New("oidc: invalid token signature")
}

// verifyWith reports whether sig is valid, and whether key can verify alg
// at all.
func verifyWith(key crypto.PublicKey, alg string, input, sig []byte) (valid, applicable bool) {
	if alg == "EdDSA" {
		pub, ok := key.(ed25519.PublicKey)
		if !ok {
			return false, false
		}
		return ed25519.Verify(pub, input, sig), true
	}
	h, err := hashFor(alg)
	if err != nil {
		return false, false
	}
	hasher := h.New()
	hasher.Write(input)
	digest := hasher.Sum(nil)
	switch alg[:2] {
	case "RS":
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return false, false
		}
		return rsa.VerifyPKCS1v15(pub, h, digest, sig) == nil, true
	case "PS":
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return false, false
		}
		return rsa.VerifyPSS(pub, h, digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil, true
	case "ES":
		pub, ok := key.(*ecdsa.PublicKey)
		// RFC 7518 §3.4 binds each ES algorithm to one curve.
		if !ok || pub.Curve.Params().Name != map[string]string{"ES256": "P-256", "ES384": "P-384", "ES512": "P-521"}[alg] {
			return false, false
		}
		size := (pub.Curve.Params().BitSize + 7) / 8
		if len(sig) != 2*size {
			return false, true
		}
		r := new(big.Int).SetBytes(sig[:size])
		sv := new(big.Int).SetBytes(sig[size:])
		return ecdsa.Verify(pub, digest, r, sv), true
	default:
		return false, false
	}
}
