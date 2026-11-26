package recoant

import (
	"fmt"
	"math"
)

// Condition restricts the candidates of a request by an attribute. It is
// data, so it can come from a configuration or an HTTP request; the
// attribute store evaluates it (see package attr). Exactly one test is set:
//
//   - In: a category attribute is one of the values; a set attribute
//     overlaps them.
//   - Min and/or Max: a number attribute lies in the range (inclusive).
//   - MaxDistanceKm: a geo attribute is at most that far from Near, or,
//     without Near, from the subject's attribute of the same name.
//   - Within: a number attribute lies between two number attributes of the
//     subject, e.g. ["min_age", "max_age"]. With Mutual the subject's
//     attribute must also lie between the candidate's two attributes, so
//     that both sides' preferences are met.
//   - Same: a category attribute equals, or a set attribute overlaps, the
//     subject's attribute of the same name.
//
// A candidate without the attribute fails the condition. A condition that
// refers to the subject's attributes is skipped if the subject lacks them,
// and the mutual check is skipped for a candidate without preferences.
type Condition struct {
	// Attribute is the attribute of the candidates.
	Attribute string `json:"attribute"`
	// In lists accepted values of a category or set attribute.
	In []string `json:"in,omitempty"`
	// Min and Max bound a number attribute.
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
	// Near is a point (latitude, longitude) for MaxDistanceKm.
	Near *[2]float64 `json:"near,omitempty"`
	// MaxDistanceKm bounds the distance of a geo attribute.
	MaxDistanceKm float64 `json:"max_distance_km,omitempty"`
	// Within names two number attributes of the subject: the lower and the
	// upper bound.
	Within []string `json:"within,omitempty"`
	// Same compares with the subject's attribute of the same name.
	Same bool `json:"same,omitempty"`
	// Mutual also checks the reverse direction of Within.
	Mutual bool `json:"mutual,omitempty"`
}

// Test names the test of a valid condition: "in", "range", "distance",
// "within" or "same".
func (c Condition) Test() string {
	switch {
	case len(c.In) > 0:
		return "in"
	case c.Min != nil || c.Max != nil:
		return "range"
	case c.MaxDistanceKm != 0 || c.Near != nil:
		return "distance"
	case len(c.Within) > 0:
		return "within"
	case c.Same:
		return "same"
	}
	return ""
}

// Validate reports an error if the condition has no attribute, not exactly
// one test, or invalid bounds. Whether the attribute exists and has a
// matching type is checked by the attribute store.
func (c Condition) Validate() error {
	if c.Attribute == "" {
		return fmt.Errorf("%w: condition without attribute", ErrInvalidRequest)
	}
	n := 0
	for _, set := range []bool{len(c.In) > 0, c.Min != nil || c.Max != nil, c.MaxDistanceKm != 0 || c.Near != nil, len(c.Within) > 0, c.Same} {
		if set {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("%w: condition on %q needs exactly one of in, min/max, max_distance_km, within or same", ErrInvalidRequest, c.Attribute)
	}
	finite := func(p *float64) bool { return p == nil || (!math.IsNaN(*p) && !math.IsInf(*p, 0)) }
	switch {
	case !finite(c.Min) || !finite(c.Max):
		return fmt.Errorf("%w: condition on %q: bounds must be finite", ErrInvalidRequest, c.Attribute)
	case c.Min != nil && c.Max != nil && *c.Min > *c.Max:
		return fmt.Errorf("%w: condition on %q: min above max", ErrInvalidRequest, c.Attribute)
	case c.Test() == "distance" && !(c.MaxDistanceKm > 0 && c.MaxDistanceKm <= 40100):
		return fmt.Errorf("%w: condition on %q: max_distance_km must be positive", ErrInvalidRequest, c.Attribute)
	case c.Near != nil && (math.IsNaN(c.Near[0]) || math.Abs(c.Near[0]) > 90 || math.IsNaN(c.Near[1]) || math.Abs(c.Near[1]) > 180):
		return fmt.Errorf("%w: condition on %q: near must be [lat, lon] in degrees", ErrInvalidRequest, c.Attribute)
	case c.Test() == "within" && (len(c.Within) != 2 || c.Within[0] == "" || c.Within[1] == ""):
		return fmt.Errorf("%w: condition on %q: within needs two attribute names", ErrInvalidRequest, c.Attribute)
	case c.Mutual && c.Test() != "within":
		return fmt.Errorf("%w: condition on %q: mutual needs within", ErrInvalidRequest, c.Attribute)
	}
	return nil
}
