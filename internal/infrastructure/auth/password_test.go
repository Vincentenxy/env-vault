package auth

import (
	"errors"
	"testing"
)

func TestPasswordHasherHashAndVerify(t *testing.T) {
	hasher, err := NewPasswordHasher()
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	first, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	second, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash second: %v", err)
	}
	if first == second {
		t.Fatal("independent salts must produce different hashes")
	}
	matched, err := hasher.Verify("correct horse battery staple", first)
	if err != nil || !matched {
		t.Fatalf("Verify correct password: matched=%v err=%v", matched, err)
	}
	matched, err = hasher.Verify("wrong password", first)
	if err != nil || matched {
		t.Fatalf("Verify wrong password: matched=%v err=%v", matched, err)
	}
}

func TestPasswordHasherDummyAndInvalidHash(t *testing.T) {
	hasher, err := NewPasswordHasher()
	if err != nil {
		t.Fatalf("NewPasswordHasher: %v", err)
	}
	matched, err := hasher.Verify("anything", "")
	if err != nil || matched {
		t.Fatalf("empty hash: matched=%v err=%v", matched, err)
	}
	if _, err := hasher.Verify("anything", "$argon2id$invalid"); !errors.Is(err, ErrInvalidPasswordHash) {
		t.Fatalf("invalid hash error = %v", err)
	}
}
