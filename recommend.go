package recoant

import (
	"context"
	"fmt"
	"time"
)

// Request asks for recommendations. The direction follows from the kind of
// the subject and the target: a user and "product" asks for products for a
// user, a product and "product" for similar products, a user and [User] for
// similar users.
type Request struct {
	// Subject is who or what to recommend for.
	Subject Ref
	// Target is the kind of the recommended references.
	Target Kind
	// K is the maximum number of results.
	K int
	// Exclude lists references that must not be recommended. The subject
	// itself is always excluded.
	Exclude []Ref
	// ExcludeSeen excludes references the subject has interacted with.
	ExcludeSeen bool
	// Filter, if set, is called for each candidate; false drops it.
	Filter func(Ref) bool
	// Time is the reference time for time-decayed scores. Zero means the
	// time of the latest signal the recommender has seen.
	Time time.Time
}

// Validate reports an error if the request cannot be answered.
func (r Request) Validate() error {
	if err := r.Subject.Validate(); err != nil {
		return fmt.Errorf("%w: subject: %w", ErrInvalidRequest, err)
	}
	if r.Target == "" {
		return fmt.Errorf("%w: empty target kind", ErrInvalidRequest)
	}
	if r.K <= 0 {
		return fmt.Errorf("%w: k must be positive, got %d", ErrInvalidRequest, r.K)
	}
	return nil
}

// Scored is a recommended reference with its score. Higher is better.
// Scores are comparable within one result, not across recommenders.
type Scored struct {
	Ref   Ref     `json:"ref"`
	Score float64 `json:"score"`
}

// Recommender answers requests. Implementations are safe for concurrent use.
type Recommender interface {
	Recommend(ctx context.Context, req Request) ([]Scored, error)
}

// Observer learns from one signal at a time (online learning).
// Implementations are safe for concurrent use with Recommend.
type Observer interface {
	Observe(sig Signal) error
}

// Seen reports whether a subject has interacted with an object. Stores
// implement it; recommenders use it for [Request.ExcludeSeen].
type Seen interface {
	Has(subject, object Ref) bool
}
