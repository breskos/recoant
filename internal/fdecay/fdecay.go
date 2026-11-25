// Package fdecay implements forward decay: values are stored relative to an
// epoch so that adding a time-decayed contribution never touches older
// values.
//
// A contribution w at time t is stored as w·e^{λ(t−t₀)}. The sum of stored
// values multiplied by e^{−λ(now−t₀)} is the decayed sum at time now.
package fdecay

import (
	"math"
	"time"
)

// maxExponent bounds λ(t−t₀) before the epoch is moved forward. e^40 ≈ 2·10^17
// keeps float32 sums of many weighted contributions far from overflow.
const maxExponent = 40

// Clock tracks the epoch of forward-decayed values.
type Clock struct {
	Rate  float64   // decay rate λ per second; 0 disables decay
	Epoch time.Time // t₀; zero until the first contribution
}

// Scale returns the stored factor for a contribution at time t, e^{λ(t−t₀)}.
// The first call sets the epoch to t.
func (c *Clock) Scale(t time.Time) float64 {
	if c.Rate == 0 {
		return 1
	}
	if c.Epoch.IsZero() {
		c.Epoch = t
	}
	return math.Exp(c.Rate * t.Sub(c.Epoch).Seconds())
}

// At returns the factor that turns stored values into values at time now,
// e^{−λ(now−t₀)}.
func (c *Clock) At(now time.Time) float64 {
	if c.Rate == 0 || c.Epoch.IsZero() {
		return 1
	}
	return math.Exp(-c.Rate * now.Sub(c.Epoch).Seconds())
}

// NeedsRebase reports whether a contribution at time t would exceed the safe
// range of stored values.
func (c *Clock) NeedsRebase(t time.Time) bool {
	return c.Rate != 0 && !c.Epoch.IsZero() && c.Rate*t.Sub(c.Epoch).Seconds() > maxExponent
}

// Rebase moves the epoch to t and returns the factor all stored values must
// be multiplied with.
func (c *Clock) Rebase(t time.Time) float64 {
	f := c.At(t)
	c.Epoch = t
	return f
}
