// Package config loads the configuration that models an application:
// entity kinds, relations, signal semantics, models and serving rules. The
// same binary can then serve a shop, a news site or a dating app.
//
// A configuration is JSON (recoant uses the standard library only),
// versioned with the "recoant" field, and validated on load; errors name
// the offending field, e.g. "signals.stars.scale: min must be below max".
// [Config.Build] turns it into an [Engine] with a store and the models.
//
// Sections planned for later versions (matching, segments, privacy, serving
// filters) are recognized and rejected with the version that introduces
// them.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Version is the configuration format version.
const Version = 1

// ErrInvalid is returned for configurations that do not validate.
var ErrInvalid = errors.New("config: invalid")

// Duration is a time.Duration written as a string: "90s", "720h" or with
// days, "30d".
type Duration time.Duration

// UnmarshalJSON parses a duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string like \"720h\" or \"30d\": %w", err)
	}
	v, err := ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON writes the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// ParseDuration parses a Go duration ("1h30m") or a number of days ("30d").
func ParseDuration(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	v, err := time.ParseDuration(s)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return v, nil
}

// Config is the configuration of an application.
type Config struct {
	// Recoant is the format version; it must be 1.
	Recoant int `json:"recoant"`
	// HalfLife is the default half-life of all relations.
	HalfLife Duration `json:"half_life,omitempty"`
	// Kinds declares the entity kinds.
	Kinds map[string]Kind `json:"kinds"`
	// Relations declares the named relations between kinds.
	Relations map[string]Relation `json:"relations"`
	// Signals describes every signal type.
	Signals map[string]Signal `json:"signals"`
	// Models declares the recommenders.
	Models map[string]Model `json:"models"`
	// Serving declares named request types.
	Serving map[string]Serving `json:"serving,omitempty"`

	// Planned sections, rejected until their version.
	Matching json.RawMessage `json:"matching,omitempty"`
	Segments json.RawMessage `json:"segments,omitempty"`
	Privacy  json.RawMessage `json:"privacy,omitempty"`
}

// Kind declares an entity kind.
type Kind struct {
	// Attributes declares the attributes of the kind. They are validated
	// now and used from v0.5.
	Attributes map[string]Attribute `json:"attributes,omitempty"`
}

// Attribute declares an attribute.
type Attribute struct {
	// Type is category, number, set or geo.
	Type string `json:"type"`
	// Sensitive marks personal data such as age or location.
	Sensitive bool `json:"sensitive,omitempty"`
}

// Relation declares a named relation.
type Relation struct {
	Subject  string             `json:"subject"`
	Object   string             `json:"object"`
	HalfLife Duration           `json:"half_life,omitempty"`
	Channels map[string]Channel `json:"channels,omitempty"`
}

// Channel configures a channel of a relation.
type Channel struct {
	// Aggregate is sum, last, max or mean.
	Aggregate string   `json:"aggregate,omitempty"`
	HalfLife  Duration `json:"half_life,omitempty"`
	NoDecay   bool     `json:"no_decay,omitempty"`
}

// Signal describes a signal type.
type Signal struct {
	// Relation is the name of the relation; required.
	Relation string `json:"relation"`
	// Feedback is implicit (default), rating or negative.
	Feedback string `json:"feedback,omitempty"`
	// Weight defaults to 1.
	Weight *float64 `json:"weight,omitempty"`
	// Transform maps the value: constant, linear, log, saturate,
	// threshold or cap, with a parameter.
	Transform *Transform `json:"transform,omitempty"`
	// Channel overrides the default channel (positive, negative, rating).
	Channel string `json:"channel,omitempty"`
	// Scale and Neutral describe ratings, e.g. [1, 5] and 3.
	Scale   []float64 `json:"scale,omitempty"`
	Neutral float64   `json:"neutral,omitempty"`
}

// Transform maps a signal value.
type Transform struct {
	Kind  string  `json:"kind"`
	Param float64 `json:"param,omitempty"`
}

// Model declares a recommender.
type Model struct {
	// Algorithm is one of popularity, item-knn, user-knn, pure-svd, mf,
	// funk-svd, biased-mf, svd++, als or bpr.
	Algorithm string `json:"algorithm"`
	// Relation is the relation to learn from; not used by popularity.
	Relation string `json:"relation,omitempty"`
	// Channel is the store channel for mf variants, e.g. "rating".
	Channel string `json:"channel,omitempty"`
	// Params are the algorithm's parameters (see its Config type).
	Params json.RawMessage `json:"params,omitempty"`
}

// Serving declares a request type.
type Serving struct {
	// Model answers the requests.
	Model string `json:"model"`
	// Target is the kind to recommend; default the object kind of the
	// model's relation.
	Target string `json:"target,omitempty"`
	// K is the default number of results; default 10.
	K int `json:"k,omitempty"`
	// ExcludeSeen excludes objects the subject interacted with.
	ExcludeSeen bool `json:"exclude_seen,omitempty"`
	// IncludeRejected allows objects the subject rejected.
	IncludeRejected bool `json:"include_rejected,omitempty"`
	// Fallback is a model used when Model returns nothing, e.g. for new
	// subjects.
	Fallback string `json:"fallback,omitempty"`
	// Filters are planned for v0.5.
	Filters json.RawMessage `json:"filters,omitempty"`
}

// Load reads and validates a configuration. Unknown fields are errors.
func Load(r io.Reader) (*Config, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// LoadFile reads and validates a configuration file.
func LoadFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	c, err := Load(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func invalid(path, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalid, path, fmt.Sprintf(format, args...))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

var algorithms = []string{"popularity", "item-knn", "user-knn", "pure-svd", "mf", "funk-svd", "biased-mf", "svd++", "als", "bpr"}

// Validate checks the configuration and returns the first error, in a
// stable order.
func (c *Config) Validate() error {
	if c.Recoant != Version {
		return invalid("recoant", "version must be %d, got %d", Version, c.Recoant)
	}
	for _, p := range []struct {
		name    string
		raw     json.RawMessage
		version string
	}{{"matching", c.Matching, "v0.6"}, {"segments", c.Segments, "v0.7"}, {"privacy", c.Privacy, "v0.5"}} {
		if len(p.raw) > 0 {
			return invalid(p.name, "supported from %s", p.version)
		}
	}
	if len(c.Kinds) == 0 {
		return invalid("kinds", "at least one kind is needed")
	}
	for _, k := range sortedKeys(c.Kinds) {
		for _, a := range sortedKeys(c.Kinds[k].Attributes) {
			switch c.Kinds[k].Attributes[a].Type {
			case "category", "number", "set", "geo":
			default:
				return invalid("kinds."+k+".attributes."+a+".type", "must be category, number, set or geo, got %q", c.Kinds[k].Attributes[a].Type)
			}
		}
	}
	for _, name := range sortedKeys(c.Relations) {
		r := c.Relations[name]
		path := "relations." + name
		if _, ok := c.Kinds[r.Subject]; !ok {
			return invalid(path+".subject", "unknown kind %q", r.Subject)
		}
		if _, ok := c.Kinds[r.Object]; !ok {
			return invalid(path+".object", "unknown kind %q", r.Object)
		}
		for _, ch := range sortedKeys(r.Channels) {
			switch r.Channels[ch].Aggregate {
			case "", "sum", "last", "max", "mean":
			default:
				return invalid(path+".channels."+ch+".aggregate", "must be sum, last, max or mean, got %q", r.Channels[ch].Aggregate)
			}
		}
	}
	for _, name := range sortedKeys(c.Signals) {
		s := c.Signals[name]
		path := "signals." + name
		if _, ok := c.Relations[s.Relation]; !ok {
			return invalid(path+".relation", "unknown relation %q", s.Relation)
		}
		switch s.Feedback {
		case "", "implicit", "rating", "negative":
		default:
			return invalid(path+".feedback", "must be implicit, rating or negative, got %q", s.Feedback)
		}
		if len(s.Scale) > 0 && (len(s.Scale) != 2 || s.Scale[0] >= s.Scale[1]) {
			return invalid(path+".scale", "must be [min, max] with min below max")
		}
	}
	if len(c.Models) == 0 {
		return invalid("models", "at least one model is needed")
	}
	for _, name := range sortedKeys(c.Models) {
		m := c.Models[name]
		path := "models." + name
		if !slices.Contains(algorithms, m.Algorithm) {
			return invalid(path+".algorithm", "must be one of %s, got %q", strings.Join(algorithms, ", "), m.Algorithm)
		}
		if m.Algorithm == "popularity" {
			continue
		}
		if _, ok := c.Relations[m.Relation]; !ok {
			return invalid(path+".relation", "unknown relation %q", m.Relation)
		}
	}
	for _, name := range sortedKeys(c.Serving) {
		s := c.Serving[name]
		path := "serving." + name
		if len(s.Filters) > 0 {
			return invalid(path+".filters", "supported from v0.5")
		}
		for field, model := range map[string]string{"model": s.Model, "fallback": s.Fallback} {
			if _, ok := c.Models[model]; !ok && (field == "model" || model != "") {
				return invalid(path+"."+field, "unknown model %q", model)
			}
		}
		if s.Target != "" {
			if _, ok := c.Kinds[s.Target]; !ok {
				return invalid(path+".target", "unknown kind %q", s.Target)
			}
		}
		if s.Target == "" && c.Models[s.Model].Algorithm == "popularity" {
			return invalid(path+".target", "needed for a popularity model")
		}
		if s.K < 0 {
			return invalid(path+".k", "must not be negative")
		}
	}
	// The signal model checks transforms, scales and channel consistency.
	if _, err := c.SignalModel(); err != nil {
		return err
	}
	return nil
}
