package login5

import (
	"crypto/sha1"
	"math/bits"
	"time"

	challengespb "github.com/badfortrains/spotcontrol/proto/spotify/login5/v3/challenges"
	"google.golang.org/protobuf/types/known/durationpb"
)

// checkHashcash verifies whether a SHA-1 hash has at least `length` trailing
// zero bits (counting from the least significant bit of the last byte).
func checkHashcash(hash []byte, length int) bool {
	idx := len(hash) - 1
	for idx >= 0 {
		zeros := bits.TrailingZeros8(hash[idx])
		if zeros >= length {
			return true
		} else if zeros < 8 {
			return false
		}

		length -= 8
		idx--
	}

	return false
}

// incrementHashcash increments the byte slice as a big-endian counter,
// carrying overflow to the left.
func incrementHashcash(data []byte, idx int) {
	data[idx]++
	if data[idx] == 0 && idx > 0 {
		incrementHashcash(data, idx-1)
	}
}

// solveHashcash solves a Login5 hashcash challenge. The algorithm concatenates
// the challenge prefix with a 16-byte suffix (seeded from the SHA-1 of the
// login context), then repeatedly SHA-1 hashes until the result has the
// required number of trailing zero bits.
func solveHashcash(loginContext []byte, challenge *challengespb.HashcashChallenge) *challengespb.HashcashSolution {
	loginContextSum := sha1.Sum(loginContext)

	suffix := make([]byte, 16)
	copy(suffix[0:8], loginContextSum[12:20])

	hasher := sha1.New()
	start := time.Now()
	for {
		hasher.Write(challenge.Prefix)
		hasher.Write(suffix)
		sum := hasher.Sum(nil)
		if checkHashcash(sum, int(challenge.Length)) {
			duration := time.Since(start)
			return &challengespb.HashcashSolution{
				Suffix: suffix,
				Duration: &durationpb.Duration{
					Seconds: int64(duration / time.Second),
					Nanos:   int32(duration % time.Second),
				},
			}
		}

		hasher.Reset()
		incrementHashcash(suffix[0:8], 7)
		incrementHashcash(suffix[8:16], 7)
	}
}
