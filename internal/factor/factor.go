// Package factor answers requests for latent factor models: every subject
// and object is a vector; recommendations are dot products, similar rows
// are cosine neighbors.
package factor

import (
	"context"
	"math"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/embed"
	"github.com/breskos/recoant/internal/linalg"
	"github.com/breskos/recoant/internal/recutil"
	"github.com/breskos/recoant/internal/topk"
	"github.com/breskos/recoant/sparse"
	"github.com/breskos/recoant/store"
)

// View is a read-only snapshot of a factor model.
type View struct {
	Store *store.Store
	Rel   store.Relation
	// Items scores objects: score = Items[j]·u + ItemBias[j].
	Items    *embed.Matrix
	ItemBias []float64 // may be nil
	// SimItems and SimItemNorms are used for similar objects; if nil, Items
	// is used.
	SimItems     *embed.Matrix
	SimItemNorms []float64
	// Users and UserNorms are used for similar subjects; if nil,
	// subject-to-subject requests are unsupported.
	Users     *embed.Matrix
	UserNorms []float64
	// UserVector returns the vector of subject si, given its history in the
	// store. ok is false if the subject has no vector.
	UserVector func(si uint32, history []sparse.Entry) (u []float64, ok bool)
	// HistoryTime is passed to store.Row; zero means the latest signal.
	HistoryTime time.Time
}

// Recommend answers subject→object, object→object and subject→subject
// requests.
func Recommend(ctx context.Context, v View, req recoant.Request) ([]recoant.Scored, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	dir := recutil.DirectionOf(v.Rel, req)
	if dir == recutil.Unsupported || (dir == recutil.SubjectToSubject && v.Users == nil) {
		return nil, recoant.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	si, ok := v.Store.Index(req.Subject)
	if !ok || v.Items == nil {
		return nil, nil
	}
	tk := topk.New(req.K)
	switch dir {
	case recutil.ObjectToObject:
		sim, norms := v.SimItems, v.SimItemNorms
		if sim == nil {
			sim, norms = v.Items, v.Items.Norms()
		}
		if int(si) >= sim.N {
			return nil, nil
		}
		sim.Similar(sim.Row(int(si)), norms[si], norms, recutil.Admit(v.Store, req, nil), tk)
	default:
		history := v.Store.Row(v.Rel, si, v.HistoryTime)
		u, ok := v.UserVector(si, history)
		if !ok {
			return nil, nil
		}
		if dir == recutil.SubjectToSubject {
			v.Users.Similar(u, linalg.Norm(u), v.UserNorms, recutil.Admit(v.Store, req, nil), tk)
			break
		}
		v.Items.Score(u, v.ItemBias, recutil.Admit(v.Store, req, history), tk)
	}
	return recutil.Results(v.Store, req.Target, tk.Sorted()), nil
}

// Interest returns the predicted interest of every subject in the object:
// the subject's trained vector (taste) times the object's vector
// (appearance) plus the object's bias. Entries are NaN for subjects or an
// object without a vector, and for references of other kinds.
func Interest(v View, subjects []recoant.Ref, object recoant.Ref) []float64 {
	out := make([]float64, len(subjects))
	for i := range out {
		out[i] = math.NaN()
	}
	if v.Items == nil || v.Users == nil || object.Kind != v.Rel.Object {
		return out
	}
	oj, ok := v.Store.Index(object)
	if !ok || int(oj) >= v.Items.N {
		return out
	}
	y, bias := v.Items.Row(int(oj)), 0.0
	if v.ItemBias != nil {
		bias = v.ItemBias[oj]
	}
	for i, s := range subjects {
		if s.Kind != v.Rel.Subject {
			continue
		}
		if si, ok := v.Store.Index(s); ok && int(si) < v.Users.N {
			out[i] = linalg.Dot(v.Users.Row(int(si)), y) + bias
		}
	}
	return out
}
