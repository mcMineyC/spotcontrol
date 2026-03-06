package dh

import (
	"testing"
)

func TestNewDiffieHellman(t *testing.T) {
	dh, err := NewDiffieHellman()
	if err != nil {
		t.Fatalf("NewDiffieHellman() error: %v", err)
	}

	if dh.privateKey == nil {
		t.Fatal("private key is nil")
	}
	if dh.publicKey == nil {
		t.Fatal("public key is nil")
	}

	pubBytes := dh.PublicKeyBytes()
	if len(pubBytes) == 0 {
		t.Fatal("public key bytes are empty")
	}

	// Public key should be at most 96 bytes (768-bit group).
	if len(pubBytes) > 96 {
		t.Fatalf("public key too large: %d bytes", len(pubBytes))
	}
}

func TestDiffieHellmanExchange(t *testing.T) {
	alice, err := NewDiffieHellman()
	if err != nil {
		t.Fatalf("NewDiffieHellman() alice error: %v", err)
	}

	bob, err := NewDiffieHellman()
	if err != nil {
		t.Fatalf("NewDiffieHellman() bob error: %v", err)
	}

	// Exchange public keys.
	aliceSecret := alice.Exchange(bob.PublicKeyBytes())
	bobSecret := bob.Exchange(alice.PublicKeyBytes())

	if len(aliceSecret) == 0 {
		t.Fatal("alice shared secret is empty")
	}
	if len(bobSecret) == 0 {
		t.Fatal("bob shared secret is empty")
	}

	// Both sides should derive the same shared secret.
	if len(aliceSecret) != len(bobSecret) {
		t.Fatalf("shared secret length mismatch: alice=%d bob=%d", len(aliceSecret), len(bobSecret))
	}
	for i := range aliceSecret {
		if aliceSecret[i] != bobSecret[i] {
			t.Fatalf("shared secret mismatch at byte %d", i)
		}
	}
}

func TestSharedSecretPanicsBeforeExchange(t *testing.T) {
	dh, err := NewDiffieHellman()
	if err != nil {
		t.Fatalf("NewDiffieHellman() error: %v", err)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from SharedSecretBytes before Exchange")
		}
	}()

	dh.SharedSecretBytes()
}

func TestSharedSecretAfterExchange(t *testing.T) {
	alice, err := NewDiffieHellman()
	if err != nil {
		t.Fatalf("NewDiffieHellman() error: %v", err)
	}

	bob, err := NewDiffieHellman()
	if err != nil {
		t.Fatalf("NewDiffieHellman() error: %v", err)
	}

	alice.Exchange(bob.PublicKeyBytes())

	secret := alice.SharedSecretBytes()
	if len(secret) == 0 {
		t.Fatal("shared secret is empty after exchange")
	}
}

func TestDifferentKeyPairs(t *testing.T) {
	dh1, err := NewDiffieHellman()
	if err != nil {
		t.Fatalf("NewDiffieHellman() 1 error: %v", err)
	}

	dh2, err := NewDiffieHellman()
	if err != nil {
		t.Fatalf("NewDiffieHellman() 2 error: %v", err)
	}

	pub1 := dh1.PublicKeyBytes()
	pub2 := dh2.PublicKeyBytes()

	// It's astronomically unlikely that two random key pairs produce the same public key.
	if len(pub1) == len(pub2) {
		same := true
		for i := range pub1 {
			if pub1[i] != pub2[i] {
				same = false
				break
			}
		}
		if same {
			t.Fatal("two independently generated key pairs produced the same public key")
		}
	}
}
