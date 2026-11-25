package popularity

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/internal/dict"
	"github.com/breskos/recoant/internal/fdecay"
	"github.com/breskos/recoant/signal"
)

// SnapshotVersion is the version of the [Snapshot] layout.
const SnapshotVersion = 1

// ErrInvalidSnapshot is returned by [FromSnapshot] for inconsistent data.
var ErrInvalidSnapshot = errors.New("popularity: invalid snapshot")

// Snapshot is the serializable state of a popularity model.
type Snapshot struct {
	Version int            `json:"version"`
	Model   signal.Model   `json:"model"`
	Config  Config         `json:"config"`
	Epoch   time.Time      `json:"epoch"`
	Latest  time.Time      `json:"latest"`
	Kinds   []KindSnapshot `json:"kinds"`
}

// KindSnapshot holds the forward-decayed scores of one kind.
type KindSnapshot struct {
	Kind   recoant.Kind `json:"kind"`
	IDs    []string     `json:"ids"`
	Scores []float64    `json:"scores"`
}

// Snapshot returns a copy of the model's state. The [recoant.Seen] source
// is not part of it.
func (p *Popularity) Snapshot() *Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	sn := &Snapshot{Version: SnapshotVersion, Model: p.model, Config: p.cfg, Epoch: p.clock.Epoch, Latest: p.latest}
	for k, d := range p.dicts {
		sn.Kinds = append(sn.Kinds, KindSnapshot{Kind: k, IDs: slices.Clone(d.IDs()), Scores: slices.Clone(p.scores[k])})
	}
	slices.SortFunc(sn.Kinds, func(a, b KindSnapshot) int { return cmp.Compare(a.Kind, b.Kind) })
	return sn
}

// FromSnapshot restores a model. The snapshot must not be modified
// afterwards.
func FromSnapshot(sn *Snapshot) (*Popularity, error) {
	if sn == nil || sn.Version != SnapshotVersion {
		return nil, fmt.Errorf("%w: missing or unsupported version", ErrInvalidSnapshot)
	}
	p, err := New(sn.Model, sn.Config)
	if err != nil {
		return nil, err
	}
	p.clock = fdecay.Clock{Rate: signal.Rate(sn.Config.HalfLife), Epoch: sn.Epoch}
	p.latest = sn.Latest
	for _, k := range sn.Kinds {
		if _, dup := p.dicts[k.Kind]; dup || len(k.IDs) != len(k.Scores) {
			return nil, fmt.Errorf("%w: kind %q duplicate or inconsistent", ErrInvalidSnapshot, k.Kind)
		}
		d, ok := dict.FromIDs(k.IDs)
		if !ok {
			return nil, fmt.Errorf("%w: duplicate id in kind %q", ErrInvalidSnapshot, k.Kind)
		}
		p.dicts[k.Kind] = d
		p.scores[k.Kind] = k.Scores
	}
	return p, nil
}
