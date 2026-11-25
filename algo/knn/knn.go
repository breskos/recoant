// Package knn implements neighborhood recommenders: item-kNN and user-kNN.
//
// Both learn from one relation of a [store.Store], e.g. user→product.
// [ItemKNN] finds, for every object, the most similar objects (objects are
// similar when the same subjects interacted with them) and recommends
// objects similar to a subject's history, or similar to an object.
// [UserKNN] finds, for every subject, the most similar subjects and
// recommends what they interacted with, or the similar subjects themselves
// (user-to-user).
//
// Training: [ItemKNN.Fit] and [UserKNN.Fit] compute all neighbor lists from
// a snapshot of the store. Refresh updates only what changed in the store
// since the last Fit or Refresh, so the models follow a stream of signals
// without full retraining. ItemKNN reads the subject's history live from the
// store, so new interactions change its recommendations immediately.
package knn

import (
	"errors"
	"fmt"
	"math"

	"github.com/breskos/recoant/store"
)

// ErrUnsupported is returned for a request whose direction the model cannot
// answer, e.g. user-to-user from an item-kNN.
var ErrUnsupported = errors.New("knn: unsupported request direction")

// Similarity selects how two rows of the interaction matrix are compared.
type Similarity string

// Similarities.
const (
	// Cosine compares the interaction strengths: a·b / (|a|·|b| + Shrink).
	// It is the default.
	Cosine Similarity = "cosine"
	// Jaccard compares only who interacted: |a ∩ b| / (|a ∪ b| + Shrink).
	Jaccard Similarity = "jaccard"
	// AdjustedCosine is Cosine after subtracting each subject's mean
	// strength. It suits explicit ratings, where a 2 is a negative signal.
	// Only positive similarities are kept.
	AdjustedCosine Similarity = "adjusted-cosine"
)

// Config configures a kNN model.
type Config struct {
	// Relation is the relation to learn from, e.g. user→product.
	Relation store.Relation `json:"relation"`
	// Similarity is the similarity measure; default Cosine.
	Similarity Similarity `json:"similarity,omitempty"`
	// Neighbors is the number of neighbors kept per row; default 50.
	Neighbors int `json:"neighbors,omitempty"`
	// Shrink is added to the denominator of the similarity. It lowers the
	// similarity of pairs with little overlap; 10 is a common value.
	Shrink float64 `json:"shrink,omitempty"`
	// MaxDegree, if positive, ignores interactions of very active rows
	// (subjects with more interactions for ItemKNN, objects with more
	// subjects for UserKNN) when computing similarities. Their cost grows
	// quadratically and they carry little information. For user-kNN with
	// hundreds of thousands of users, set it (e.g. 1000): on 200,000 users
	// with Zipf-popular items it cuts Refresh after 2,000 new signals from
	// about 8 s to under 1 s.
	MaxDegree int `json:"max_degree,omitempty"`
	// Workers is the number of parallel workers; default the number of
	// CPUs.
	Workers int `json:"workers,omitempty"`
}

func (c Config) withDefaults() (Config, error) {
	if c.Relation.Subject == "" || c.Relation.Object == "" {
		return c, fmt.Errorf("knn: relation %q incomplete", c.Relation)
	}
	if c.Similarity == "" {
		c.Similarity = Cosine
	}
	switch c.Similarity {
	case Cosine, Jaccard, AdjustedCosine:
	default:
		return c, fmt.Errorf("knn: unknown similarity %q", c.Similarity)
	}
	if c.Neighbors == 0 {
		c.Neighbors = 50
	}
	if c.Neighbors < 0 || c.MaxDegree < 0 || c.Workers < 0 {
		return c, fmt.Errorf("knn: negative neighbors, max degree or workers")
	}
	if c.Shrink < 0 || math.IsNaN(c.Shrink) || math.IsInf(c.Shrink, 0) {
		return c, fmt.Errorf("knn: invalid shrink %v", c.Shrink)
	}
	return c, nil
}

// Neighbor is a similar row and its similarity.
type Neighbor struct {
	Index uint32
	Sim   float32
}
