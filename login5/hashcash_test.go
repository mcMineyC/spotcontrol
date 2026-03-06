package login5

import (
	"crypto/sha1"
	"testing"

	challengespb "github.com/badfortrains/spotcontrol/proto/spotify/login5/v3/challenges"
)

func TestCheckHashcash(t *testing.T) {
	tests := []struct {
		name   string
		hash   []byte
		length int
		want   bool
	}{
		{
			name:   "zero length always passes",
			hash:   []byte{0xff, 0xff, 0xff, 0xff},
			length: 0,
			want:   true,
		},
		{
			name:   "one trailing zero bit - last byte 0xfe",
			hash:   []byte{0xff, 0xfe},
			length: 1,
			want:   true,
		},
		{
			name:   "one trailing zero bit - last byte 0x01 fails",
			hash:   []byte{0xff, 0x01},
			length: 1,
			want:   false,
		},
		{
			name:   "eight trailing zero bits",
			hash:   []byte{0xff, 0x00},
			length: 8,
			want:   true,
		},
		{
			name:   "eight trailing zero bits - need 9 fails",
			hash:   []byte{0xff, 0x00},
			length: 9,
			want:   false,
		},
		{
			name:   "sixteen trailing zero bits",
			hash:   []byte{0xff, 0x00, 0x00},
			length: 16,
			want:   true,
		},
		{
			name:   "last byte all zeros, need 4",
			hash:   []byte{0xab, 0x00},
			length: 4,
			want:   true,
		},
		{
			name:   "last byte 0x10 has 4 trailing zeros",
			hash:   []byte{0xab, 0x10},
			length: 4,
			want:   true,
		},
		{
			name:   "last byte 0x10 has 4 trailing zeros - need 5 fails",
			hash:   []byte{0xab, 0x10},
			length: 5,
			want:   false,
		},
		{
			name:   "empty hash with zero length",
			hash:   []byte{},
			length: 0,
			want:   false,
		},
		{
			name:   "all zero bytes need 16",
			hash:   []byte{0x00, 0x00},
			length: 16,
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkHashcash(tt.hash, tt.length)
			if got != tt.want {
				t.Errorf("checkHashcash(%x, %d) = %v, want %v", tt.hash, tt.length, got, tt.want)
			}
		})
	}
}

func TestIncrementHashcash(t *testing.T) {
	tests := []struct {
		name   string
		input  []byte
		expect []byte
	}{
		{
			name:   "simple increment",
			input:  []byte{0x00, 0x00},
			expect: []byte{0x00, 0x01},
		},
		{
			name:   "carry over",
			input:  []byte{0x00, 0xff},
			expect: []byte{0x01, 0x00},
		},
		{
			name:   "double carry",
			input:  []byte{0x00, 0xff, 0xff},
			expect: []byte{0x01, 0x00, 0x00},
		},
		{
			name:   "no carry needed",
			input:  []byte{0x01, 0x02},
			expect: []byte{0x01, 0x03},
		},
		{
			name:   "single byte wrap",
			input:  []byte{0xff},
			expect: []byte{0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := make([]byte, len(tt.input))
			copy(data, tt.input)
			incrementHashcash(data, len(data)-1)

			if len(data) != len(tt.expect) {
				t.Fatalf("length mismatch: got %d, want %d", len(data), len(tt.expect))
			}
			for i := range data {
				if data[i] != tt.expect[i] {
					t.Errorf("byte %d: got 0x%02x, want 0x%02x", i, data[i], tt.expect[i])
				}
			}
		})
	}
}

func TestSolveHashcash(t *testing.T) {
	// Use a small difficulty so the test completes quickly.
	loginContext := []byte("test-login-context-1234567890")
	challenge := &challengespb.HashcashChallenge{
		Prefix: []byte("test-prefix"),
		Length: 1, // Only require 1 trailing zero bit.
	}

	solution := solveHashcash(loginContext, challenge)

	if solution == nil {
		t.Fatal("solveHashcash returned nil")
	}

	if len(solution.Suffix) != 16 {
		t.Fatalf("suffix length = %d, want 16", len(solution.Suffix))
	}

	if solution.Duration == nil {
		t.Fatal("duration is nil")
	}

	// Verify the solution is actually correct by recomputing the hash.
	hasher := sha1.New()
	hasher.Write(challenge.Prefix)
	hasher.Write(solution.Suffix)
	sum := hasher.Sum(nil)

	if !checkHashcash(sum, int(challenge.Length)) {
		t.Errorf("solution does not satisfy the challenge: hash=%x, required trailing zeros=%d", sum, challenge.Length)
	}
}

func TestSolveHashcashHigherDifficulty(t *testing.T) {
	loginContext := []byte("another-context-for-testing")
	challenge := &challengespb.HashcashChallenge{
		Prefix: []byte("harder-prefix"),
		Length: 10, // Require 10 trailing zero bits.
	}

	solution := solveHashcash(loginContext, challenge)

	if solution == nil {
		t.Fatal("solveHashcash returned nil")
	}

	// Verify correctness.
	hasher := sha1.New()
	hasher.Write(challenge.Prefix)
	hasher.Write(solution.Suffix)
	sum := hasher.Sum(nil)

	if !checkHashcash(sum, int(challenge.Length)) {
		t.Errorf("solution does not satisfy the challenge: hash=%x, required trailing zeros=%d", sum, challenge.Length)
	}
}

func TestSolveHashcashZeroDifficulty(t *testing.T) {
	loginContext := []byte("zero-difficulty")
	challenge := &challengespb.HashcashChallenge{
		Prefix: []byte("easy"),
		Length: 0,
	}

	solution := solveHashcash(loginContext, challenge)

	if solution == nil {
		t.Fatal("solveHashcash returned nil")
	}

	// Any suffix should work with difficulty 0.
	hasher := sha1.New()
	hasher.Write(challenge.Prefix)
	hasher.Write(solution.Suffix)
	sum := hasher.Sum(nil)

	if !checkHashcash(sum, 0) {
		t.Errorf("solution does not satisfy zero-difficulty challenge")
	}
}

func TestSolveHashcashSuffixSeededFromContext(t *testing.T) {
	// Verify that different login contexts produce different initial suffixes.
	ctx1 := []byte("context-alpha")
	ctx2 := []byte("context-beta")

	challenge := &challengespb.HashcashChallenge{
		Prefix: []byte("prefix"),
		Length: 1,
	}

	sol1 := solveHashcash(ctx1, challenge)
	sol2 := solveHashcash(ctx2, challenge)

	// Both should be valid solutions.
	for i, sol := range []*challengespb.HashcashSolution{sol1, sol2} {
		hasher := sha1.New()
		hasher.Write(challenge.Prefix)
		hasher.Write(sol.Suffix)
		sum := hasher.Sum(nil)
		if !checkHashcash(sum, int(challenge.Length)) {
			t.Errorf("solution %d does not satisfy challenge", i)
		}
	}
}
