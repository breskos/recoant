// Package dict interns string IDs into dense uint32 indexes.
package dict

// Dictionary maps IDs to indexes 0, 1, 2, … in order of first appearance.
// It is not safe for concurrent use.
type Dictionary struct {
	index map[string]uint32
	ids   []string
}

// New returns an empty dictionary.
func New() *Dictionary { return &Dictionary{index: make(map[string]uint32)} }

// FromIDs returns a dictionary with the given IDs at their positions. ok is
// false if an ID occurs twice.
func FromIDs(ids []string) (d *Dictionary, ok bool) {
	d = &Dictionary{index: make(map[string]uint32, len(ids)), ids: ids}
	for i, id := range ids {
		if _, dup := d.index[id]; dup {
			return nil, false
		}
		d.index[id] = uint32(i)
	}
	return d, true
}

// Intern returns the index of id, adding it if needed.
func (d *Dictionary) Intern(id string) uint32 {
	if i, ok := d.index[id]; ok {
		return i
	}
	i := uint32(len(d.ids))
	d.index[id] = i
	d.ids = append(d.ids, id)
	return i
}

// Lookup returns the index of id.
func (d *Dictionary) Lookup(id string) (uint32, bool) {
	i, ok := d.index[id]
	return i, ok
}

// ID returns the ID at index i. i must be smaller than Len.
func (d *Dictionary) ID(i uint32) string { return d.ids[i] }

// Len returns the number of IDs.
func (d *Dictionary) Len() int { return len(d.ids) }

// IDs returns the IDs in index order. The slice must not be modified.
func (d *Dictionary) IDs() []string { return d.ids }
