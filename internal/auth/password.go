package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

const (
	hashArgon2id = "argon2id"
	hashBcrypt   = "bcrypt"
)

const (
	// maxArgon2MemoryKiB bounds the memory (KiB) a stored hash may demand at
	// verification time. The hasher default is 64 MiB; 1 GiB still accepts
	// hashes from other systems while blocking multi-TiB allocation bombs.
	maxArgon2MemoryKiB uint32 = 1 << 20
	// maxArgon2TimeCost bounds the CPU passes a stored hash may demand at
	// verification time.
	maxArgon2TimeCost uint32 = 16
)

// validArgon2Params reports whether the declared argon2id parameters are
// safely verifiable. argon2.IDKey allocates memory KiB and loops timeCost
// passes on every verification; its only internal guard is a lower memory
// clamp (8*parallelism KiB), so upper bounds must be enforced here before a
// hash is stored or verified.
func validArgon2Params(memory uint32, timeCost uint32, parallelism uint8) bool {
	if memory == 0 || timeCost == 0 || parallelism == 0 {
		return false
	}
	if uint64(memory) < 8*uint64(parallelism) {
		return false
	}
	return memory <= maxArgon2MemoryKiB && timeCost <= maxArgon2TimeCost
}

func HashPassword(password, algo string) (string, error) {
	switch algo {
	case hashArgon2id:
		return hashArgon2ID(password)
	case hashBcrypt:
		out, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		return string(out), err
	default:
		return "", fmt.Errorf("unknown hash algorithm: %s", algo)
	}
}

func VerifyPassword(password, encoded string) bool {
	if strings.HasPrefix(encoded, "$argon2id$") {
		return verifyArgon2ID(password, encoded)
	}
	if strings.HasPrefix(encoded, "$2a$") || strings.HasPrefix(encoded, "$2b$") || strings.HasPrefix(encoded, "$2y$") {
		return bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password)) == nil
	}
	return false
}

func ValidatePasswordHash(encoded string) error {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return errors.New("password hash is empty")
	}
	if strings.HasPrefix(encoded, "$argon2id$") {
		memory, timeCost, parallelism, salt, sum, err := parseArgon2ID(encoded)
		if err != nil {
			return err
		}
		if !validArgon2Params(memory, timeCost, parallelism) || len(salt) == 0 || len(sum) == 0 {
			return errors.New("invalid argon2id parameters")
		}
		return nil
	}
	if strings.HasPrefix(encoded, "$2a$") || strings.HasPrefix(encoded, "$2b$") || strings.HasPrefix(encoded, "$2y$") {
		if _, err := bcrypt.Cost([]byte(encoded)); err != nil {
			return err
		}
		return nil
	}
	return errors.New("unsupported password hash format")
}

func hashArgon2ID(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	memory := uint32(64 * 1024)
	timeCost := uint32(2)
	parallelism := uint8(1)
	keyLen := uint32(32)
	hash := argon2.IDKey([]byte(password), salt, timeCost, memory, parallelism, keyLen)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		memory,
		timeCost,
		parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func verifyArgon2ID(password, encoded string) bool {
	mem, tc, p, salt, sum, err := parseArgon2ID(encoded)
	if err != nil {
		return false
	}
	if !validArgon2Params(mem, tc, p) {
		return false
	}
	if len(sum) == 0 || uint64(len(sum)) > uint64(^uint32(0)) {
		return false
	}
	// #nosec G115 -- len(sum) is bounded to uint32 range above.
	keyLen := uint32(len(sum))
	calc := argon2.IDKey([]byte(password), salt, tc, mem, p, keyLen)
	return subtle.ConstantTimeCompare(calc, sum) == 1
}

func parseArgon2ID(encoded string) (memory uint32, timeCost uint32, parallelism uint8, salt []byte, sum []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2id encoding")
	}
	var version int
	if _, scanErr := fmt.Sscanf(parts[2], "v=%d", &version); scanErr != nil || version != 19 {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2id version")
	}
	var pInt uint32
	if _, scanErr := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &pInt); scanErr != nil {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2id params")
	}
	if pInt > 255 {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2id parallelism")
	}
	parallelism = uint8(pInt)
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2id salt")
	}
	sum, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2id hash")
	}
	return
}
