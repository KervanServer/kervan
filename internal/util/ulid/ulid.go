package ulid

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	mu   sync.Mutex
	last uint64
)

// New returns a monotonic ULID-compatible identifier.
func New() string {
	mu.Lock()
	defer mu.Unlock()

	now := uint64(time.Now().UTC().UnixMilli())
	if now <= last {
		now = last + 1
	}
	last = now

	var entropy [10]byte
	_, _ = rand.Read(entropy[:])

	var raw [16]byte
	raw[0] = byte(now >> 40)
	raw[1] = byte(now >> 32)
	raw[2] = byte(now >> 24)
	raw[3] = byte(now >> 16)
	raw[4] = byte(now >> 8)
	raw[5] = byte(now)
	copy(raw[6:], entropy[:])

	return encode(raw)
}

func encode(raw [16]byte) string {
	// Encodes 128 bits into 26 Crockford Base32 chars.
	out := make([]byte, 26)
	v0 := binary.BigEndian.Uint64(raw[0:8])
	v1 := binary.BigEndian.Uint64(raw[8:16])

	// 128-bit bit-slicing tailored for ULID.
	out[25] = crockford[v1&31]
	out[24] = crockford[(v1>>5)&31]
	out[23] = crockford[(v1>>10)&31]
	out[22] = crockford[(v1>>15)&31]
	out[21] = crockford[(v1>>20)&31]
	out[20] = crockford[(v1>>25)&31]
	out[19] = crockford[(v1>>30)&31]
	out[18] = crockford[(v1>>35)&31]
	out[17] = crockford[(v1>>40)&31]
	out[16] = crockford[(v1>>45)&31]
	out[15] = crockford[(v1>>50)&31]
	out[14] = crockford[(v1>>55)&31]
	out[13] = crockford[((v1>>60)&15)|((v0&1)<<4)]
	out[12] = crockford[(v0>>1)&31]
	out[11] = crockford[(v0>>6)&31]
	out[10] = crockford[(v0>>11)&31]
	out[9] = crockford[(v0>>16)&31]
	out[8] = crockford[(v0>>21)&31]
	out[7] = crockford[(v0>>26)&31]
	out[6] = crockford[(v0>>31)&31]
	out[5] = crockford[(v0>>36)&31]
	out[4] = crockford[(v0>>41)&31]
	out[3] = crockford[(v0>>46)&31]
	out[2] = crockford[(v0>>51)&31]
	out[1] = crockford[(v0>>56)&31]
	out[0] = crockford[(v0>>61)&31]
	return string(out)
}
