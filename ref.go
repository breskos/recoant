package recoant

import "fmt"

// Kind names a type of node: users and the kinds of entities, for example
// "article", "product" or "category".
type Kind string

// User is the kind of users. Any other kind can be used as well; recoant
// treats all kinds the same.
const User Kind = "user"

// Ref references a user or an entity.
type Ref struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"` // opaque, already anonymized
}

// U returns a reference to the user with the given ID.
func U(id string) Ref { return Ref{Kind: User, ID: id} }

// String returns "kind:id".
func (r Ref) String() string { return string(r.Kind) + ":" + r.ID }

// Validate reports an error if the kind or the ID is empty.
func (r Ref) Validate() error {
	if r.Kind == "" {
		return fmt.Errorf("%w: empty kind (id %q)", ErrInvalidRef, r.ID)
	}
	if r.ID == "" {
		return fmt.Errorf("%w: empty id (kind %q)", ErrInvalidRef, r.Kind)
	}
	return nil
}
