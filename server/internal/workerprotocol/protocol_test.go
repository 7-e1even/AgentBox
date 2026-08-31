package workerprotocol

import (
	"errors"
	"testing"
)

func TestNegotiate(t *testing.T) {
	for _, test := range []struct {
		name, minimum, maximum string
		want                   int
		err                    error
	}{
		{name: "current", minimum: "1", maximum: "1", want: 1},
		{name: "newer compatible Worker", minimum: "1", maximum: "2", want: 1},
		{name: "n-1 Worker", want: 1},
		{name: "missing minimum", maximum: "1", err: ErrInvalid},
		{name: "missing maximum", minimum: "1", err: ErrInvalid},
		{name: "non numeric", minimum: "one", maximum: "1", err: ErrInvalid},
		{name: "reversed", minimum: "2", maximum: "1", err: ErrInvalid},
		{name: "zero", minimum: "0", maximum: "1", err: ErrInvalid},
		{name: "negative", minimum: "-1", maximum: "1", err: ErrInvalid},
		{name: "incompatible", minimum: "2", maximum: "3", err: ErrIncompatible},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Negotiate(test.minimum, test.maximum)
			if got != test.want || !errors.Is(err, test.err) {
				t.Fatalf("Negotiate = (%d, %v), want (%d, %v)", got, err, test.want, test.err)
			}
		})
	}
}

func TestValidateSelection(t *testing.T) {
	for _, selected := range []string{"", "1"} {
		if got, err := ValidateSelection(selected); err != nil || got != 1 {
			t.Fatalf("selection %q = (%d, %v)", selected, got, err)
		}
	}
	for _, selected := range []string{"0", "2", "-1", "unknown"} {
		if _, err := ValidateSelection(selected); !errors.Is(err, ErrIncompatible) {
			t.Fatalf("selection %q: %v, want incompatible", selected, err)
		}
	}
}
