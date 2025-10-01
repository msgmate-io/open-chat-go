package user

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"math"
	"strconv"
	"strings"
	"time"
)

func hotp(secret []byte, counter uint64, digits int) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0F
	code := (uint32(sum[offset])&0x7F)<<24 |
		(uint32(sum[offset+1])&0xFF)<<16 |
		(uint32(sum[offset+2])&0xFF)<<8 |
		(uint32(sum[offset+3]) & 0xFF)
	mod := uint32(math.Pow10(digits))
	val := code % mod
	s := strconv.FormatUint(uint64(val), 10)
	if len(s) < digits {
		s = strings.Repeat("0", digits-len(s)) + s
	}
	return s
}

// VerifyTOTP verifies a TOTP code for a base32 secret using a +/-1 time-step window.
func VerifyTOTP(secretBase32 string, code string, t time.Time) bool {
	normalized := strings.ToUpper(strings.ReplaceAll(secretBase32, " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized)
	if err != nil {
		return false
	}
	timestep := uint64(30)
	counter := uint64(t.Unix()) / timestep
	digits := 6
	// allow small clock skew
	for _, c := range []uint64{counter - 1, counter, counter + 1} {
		if hotp(key, c, digits) == code {
			return true
		}
	}
	return false
}
