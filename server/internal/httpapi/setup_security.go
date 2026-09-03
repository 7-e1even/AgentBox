package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
)

const setupCodeBytes = 24

var (
	errSetupCodeInvalid     = errors.New("setup code is invalid")
	errSetupCodeFormat      = errors.New("setup code must be unpadded base64url encoding of at least 24 bytes")
	errSetupCodeUnavailable = errors.New("setup code is unavailable")
	errSetupBusy            = errors.New("setup is already in progress")
)

type setupGate struct {
	mu       sync.Mutex
	hash     [sha256.Size]byte
	required bool
	consumed bool
	active   chan struct{}
}

func newSetupGate(code string) (*setupGate, error) {
	gate := &setupGate{active: make(chan struct{}, 1)}
	if code == "" {
		return gate, nil
	}
	if err := ValidateSetupCode(code); err != nil {
		return gate, err
	}
	gate.hash = sha256.Sum256([]byte(code))
	gate.required = true
	return gate, nil
}

func GenerateSetupCode() (string, error) {
	random := make([]byte, setupCodeBytes)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

// ValidateSetupCode enforces the same unpadded base64url representation used
// by GenerateSetupCode. It cannot prove operator-supplied randomness, but it
// rejects short or malformed overrides before they can protect public setup.
func ValidateSetupCode(code string) error {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(code)
	if err != nil || len(decoded) < setupCodeBytes {
		return errSetupCodeFormat
	}
	return nil
}

func (gate *setupGate) acquire(code string) error {
	candidate := sha256.Sum256([]byte(code))
	gate.mu.Lock()
	valid := gate.required && !gate.consumed && subtle.ConstantTimeCompare(candidate[:], gate.hash[:]) == 1
	available := gate.required && !gate.consumed
	gate.mu.Unlock()
	if !available {
		return errSetupCodeUnavailable
	}
	if !valid {
		return errSetupCodeInvalid
	}
	select {
	case gate.active <- struct{}{}:
		gate.mu.Lock()
		defer gate.mu.Unlock()
		if gate.consumed {
			<-gate.active
			return errSetupCodeUnavailable
		}
		return nil
	default:
		return errSetupBusy
	}
}

func (gate *setupGate) release(success bool) {
	gate.mu.Lock()
	if success {
		gate.consumed = true
		gate.hash = [sha256.Size]byte{}
	}
	gate.mu.Unlock()
	<-gate.active
}
