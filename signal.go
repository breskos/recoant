package recoant

import (
	"fmt"
	"math"
	"time"
)

// SignalType names a kind of interaction, such as a visit or a purchase.
// The constants below are common types; any other name can be used.
type SignalType string

// Common signal types, ordered from shallow to deep.
const (
	Visit    SignalType = "visit"
	Click    SignalType = "click"
	Dwell    SignalType = "dwell" // Value holds the dwell time in seconds
	Cart     SignalType = "cart"
	Purchase SignalType = "purchase"
)

// Signal is one observed interaction of a subject (usually a user) with an
// object (an entity or another user).
type Signal struct {
	Subject Ref        `json:"subject"`
	Object  Ref        `json:"object"`
	Type    SignalType `json:"type"`
	// Value carries a magnitude, e.g. the dwell time in seconds or a
	// quantity. Use 1 when the type has no magnitude.
	Value   float64   `json:"value"`
	Time    time.Time `json:"time"`
	Session string    `json:"session,omitempty"`
}

// Validate reports an error if the signal is incomplete or its value is not
// a finite number.
func (s Signal) Validate() error {
	if err := s.Subject.Validate(); err != nil {
		return fmt.Errorf("%w: subject: %w", ErrInvalidSignal, err)
	}
	if err := s.Object.Validate(); err != nil {
		return fmt.Errorf("%w: object: %w", ErrInvalidSignal, err)
	}
	if s.Type == "" {
		return fmt.Errorf("%w: empty type", ErrInvalidSignal)
	}
	if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
		return fmt.Errorf("%w: value %v", ErrInvalidSignal, s.Value)
	}
	if s.Time.IsZero() {
		return fmt.Errorf("%w: zero time", ErrInvalidSignal)
	}
	return nil
}
