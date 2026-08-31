// Package workerprotocol defines the wire contract shared by the Server and
// Worker. Protocol versions are independent of application release versions.
package workerprotocol

import (
	"errors"
	"fmt"
	"strconv"
)

const (
	Minimum = 1
	Current = 1

	HeaderMinimum  = "X-AgentBox-Worker-Protocol-Min"
	HeaderMaximum  = "X-AgentBox-Worker-Protocol-Max"
	HeaderSelected = "X-AgentBox-Worker-Protocol"
)

var (
	ErrInvalid      = errors.New("invalid Worker protocol range")
	ErrIncompatible = errors.New("Worker protocol is incompatible")
)

// Negotiate chooses the highest shared version. Missing headers are the
// explicitly supported n-1 contract: the same v1 payloads, before negotiation
// headers were introduced. This does not relax lease-generation validation.
func Negotiate(minimum, maximum string) (int, error) {
	if minimum == "" && maximum == "" {
		return 1, nil
	}
	low, errLow := strconv.Atoi(minimum)
	high, errHigh := strconv.Atoi(maximum)
	if errLow != nil || errHigh != nil || low < 1 || high < low {
		return 0, ErrInvalid
	}
	selected := min(high, Current)
	if selected < max(low, Minimum) {
		return 0, fmt.Errorf("%w: Server supports %d-%d, Worker offered %d-%d", ErrIncompatible, Minimum, Current, low, high)
	}
	return selected, nil
}

// ValidateSelection also accepts a missing header from an n-1 Server, whose
// existing v1 payloads predate explicit negotiation.
func ValidateSelection(value string) (int, error) {
	if value == "" {
		return 1, nil
	}
	selected, err := strconv.Atoi(value)
	if err != nil || selected < Minimum || selected > Current {
		return 0, fmt.Errorf("%w: Server selected %q", ErrIncompatible, value)
	}
	return selected, nil
}
