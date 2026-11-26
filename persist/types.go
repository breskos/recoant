package persist

import (
	"github.com/breskos/recoant/algo/popularity"
	"github.com/breskos/recoant/attr"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

// Kinds of content.
const (
	KindStore       = "store"
	KindSignalModel = "signal-model"
	KindPopularity  = "popularity"
	KindAttributes  = "attributes"
)

// SaveStore writes a store to path.
func SaveStore(path string, f Format, s *store.Store) error {
	return Save(path, f, KindStore, s.Snapshot())
}

// LoadStore reads a store from path.
func LoadStore(path string) (*store.Store, error) {
	var sn store.Snapshot
	if _, err := Load(path, KindStore, &sn); err != nil {
		return nil, err
	}
	return store.FromSnapshot(&sn)
}

// SaveSignalModel writes a signal model to path.
func SaveSignalModel(path string, f Format, m signal.Model) error {
	return Save(path, f, KindSignalModel, m)
}

// LoadSignalModel reads a signal model from path and validates it.
func LoadSignalModel(path string) (signal.Model, error) {
	var m signal.Model
	if _, err := Load(path, KindSignalModel, &m); err != nil {
		return signal.Model{}, err
	}
	return m, m.Validate()
}

// SavePopularity writes a popularity model to path.
func SavePopularity(path string, f Format, p *popularity.Popularity) error {
	return Save(path, f, KindPopularity, p.Snapshot())
}

// LoadPopularity reads a popularity model from path.
func LoadPopularity(path string) (*popularity.Popularity, error) {
	var sn popularity.Snapshot
	if _, err := Load(path, KindPopularity, &sn); err != nil {
		return nil, err
	}
	return popularity.FromSnapshot(&sn)
}

// SaveAttributes writes an attribute store to path, including sensitive
// attributes.
func SaveAttributes(path string, f Format, s *attr.Store) error {
	return Save(path, f, KindAttributes, s.Snapshot())
}

// LoadAttributes reads an attribute store from path.
func LoadAttributes(path string) (*attr.Store, error) {
	var sn attr.Snapshot
	if _, err := Load(path, KindAttributes, &sn); err != nil {
		return nil, err
	}
	return attr.FromSnapshot(&sn)
}
