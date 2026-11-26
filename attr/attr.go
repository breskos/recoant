// Package attr holds the attributes of users and entities: their schema,
// a store fed by events, and filters on them.
//
// Every kind declares its attributes in a [Schema]: a category (city,
// brand), a number (age, price), a set (tags, interests) or a geo point
// (location). Attributes marked sensitive — age, gender, location — are
// personal data even with anonymized IDs: [Store.Attributes] leaves them
// out unless asked, and no error message of this package contains an
// attribute value.
//
// Attributes arrive as events, like signals ([Store.Apply]): set (also to
// update) and delete, for one attribute or a whole entity. The [Store]
// keeps one dense column per attribute, so millions of entities stay
// compact.
//
// Requests restrict candidates with conditions as data
// ([recoant.Request.Where]). [Filtered] wraps any recommender and evaluates
// them, including mutual conditions for matching, where both sides'
// preferences must be met.
package attr

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"

	"github.com/breskos/recoant"
)

var (
	// ErrInvalidSchema is returned for schemas with unknown types or empty
	// names.
	ErrInvalidSchema = errors.New("attr: invalid schema")
	// ErrInvalid is returned for events and values that do not fit the
	// schema.
	ErrInvalid = errors.New("attr: invalid value")
	// ErrUnknown is returned for attributes the schema does not declare.
	ErrUnknown = errors.New("attr: unknown attribute")
	// ErrCondition is returned for conditions that do not fit the schema.
	ErrCondition = errors.New("attr: invalid condition")
)

// Type is the type of an attribute.
type Type string

// Attribute types.
const (
	// Category is one value out of many, e.g. a city or a brand.
	Category Type = "category"
	// Number is a finite number, e.g. an age or a price.
	Number Type = "number"
	// Set is a set of values, e.g. tags or interests.
	Set Type = "set"
	// Geo is a point on earth, e.g. a location.
	Geo Type = "geo"
)

// Spec declares an attribute.
type Spec struct {
	Type Type `json:"type"`
	// Sensitive marks personal data such as age, gender or location.
	Sensitive bool `json:"sensitive,omitempty"`
}

// Schema declares the attributes of every kind.
type Schema map[recoant.Kind]map[string]Spec

// Validate reports an error for empty names or unknown types.
func (s Schema) Validate() error {
	for kind, attrs := range s {
		if kind == "" {
			return fmt.Errorf("%w: empty kind", ErrInvalidSchema)
		}
		for name, spec := range attrs {
			if name == "" {
				return fmt.Errorf("%w: %s: empty attribute name", ErrInvalidSchema, kind)
			}
			switch spec.Type {
			case Category, Number, Set, Geo:
			default:
				return fmt.Errorf("%w: %s.%s: unknown type %q", ErrInvalidSchema, kind, name, spec.Type)
			}
		}
	}
	return nil
}

// Spec returns the declaration of an attribute.
func (s Schema) Spec(kind recoant.Kind, name string) (Spec, bool) {
	spec, ok := s[kind][name]
	return spec, ok
}

// Point is a location in degrees.
type Point struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// earthRadiusKm is the mean radius of the earth.
const earthRadiusKm = 6371.0088

// DistanceKm returns the great-circle distance between two points.
func DistanceKm(a, b Point) float64 {
	rad := math.Pi / 180
	dLat, dLon := (b.Lat-a.Lat)*rad, (b.Lon-a.Lon)*rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(min(h, 1)))
}

// Value is the value of an attribute. Type selects the field that holds
// it. In JSON a value is written in its natural form: a string (category),
// a number, an array of strings (set) or {"lat": …, "lon": …} (geo).
type Value struct {
	Type     Type
	Category string
	Number   float64
	Set      []string
	Geo      Point
}

// Cat returns a category value.
func Cat(v string) Value { return Value{Type: Category, Category: v} }

// Num returns a number value.
func Num(v float64) Value { return Value{Type: Number, Number: v} }

// Tags returns a set value.
func Tags(v ...string) Value { return Value{Type: Set, Set: v} }

// At returns a geo value.
func At(lat, lon float64) Value { return Value{Type: Geo, Geo: Point{Lat: lat, Lon: lon}} }

// IsZero reports whether the value is unset.
func (v Value) IsZero() bool { return v.Type == "" }

// Validate reports an error if the value does not have type t or is
// invalid: an empty category or set element, a number that is not finite,
// or a point outside [-90, 90] × [-180, 180]. The error does not contain
// the value.
func (v Value) Validate(t Type) error {
	if v.Type != t {
		return fmt.Errorf("%w: %s value for a %s attribute", ErrInvalid, v.Type, t)
	}
	switch t {
	case Category:
		if v.Category == "" {
			return fmt.Errorf("%w: empty category", ErrInvalid)
		}
	case Number:
		if math.IsNaN(v.Number) || math.IsInf(v.Number, 0) {
			return fmt.Errorf("%w: number is not finite", ErrInvalid)
		}
	case Set:
		if slices.Contains(v.Set, "") {
			return fmt.Errorf("%w: empty set element", ErrInvalid)
		}
	case Geo:
		if !(math.Abs(v.Geo.Lat) <= 90 && math.Abs(v.Geo.Lon) <= 180) {
			return fmt.Errorf("%w: point outside [-90, 90] × [-180, 180]", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown type %q", ErrInvalid, t)
	}
	return nil
}

// MarshalJSON writes the value in its natural form.
func (v Value) MarshalJSON() ([]byte, error) {
	switch v.Type {
	case Category:
		return json.Marshal(v.Category)
	case Number:
		return json.Marshal(v.Number)
	case Set:
		if v.Set == nil {
			return []byte("[]"), nil
		}
		return json.Marshal(v.Set)
	case Geo:
		return json.Marshal(v.Geo)
	}
	return []byte("null"), nil
}

// UnmarshalJSON reads a value and infers its type from the JSON form.
func (v *Value) UnmarshalJSON(b []byte) error {
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	switch x := raw.(type) {
	case nil:
		*v = Value{}
	case string:
		*v = Cat(x)
	case float64:
		*v = Num(x)
	case []any:
		set := make([]string, len(x))
		for i, e := range x {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("%w: set elements must be strings", ErrInvalid)
			}
			set[i] = s
		}
		*v = Tags(set...)
	default:
		var p struct {
			Lat *float64 `json:"lat"`
			Lon *float64 `json:"lon"`
		}
		if err := json.Unmarshal(b, &p); err != nil || p.Lat == nil || p.Lon == nil {
			return fmt.Errorf("%w: a value is a string, number, array of strings or {\"lat\", \"lon\"}", ErrInvalid)
		}
		*v = At(*p.Lat, *p.Lon)
	}
	return nil
}

// Op is the operation of an event.
type Op string

// Operations.
const (
	// OpSet sets (or updates) an attribute. It is the default.
	OpSet Op = "set"
	// OpDelete removes an attribute, or, without an attribute name, all
	// attributes of the entity.
	OpDelete Op = "delete"
)

// Event changes the attributes of an entity.
type Event struct {
	Ref       recoant.Ref `json:"ref"`
	Op        Op          `json:"op,omitempty"`
	Attribute string      `json:"attribute,omitempty"`
	Value     Value       `json:"value,omitzero"`
}
