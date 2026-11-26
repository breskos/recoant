// Package dict interns string IDs into dense uint32 indexes.
package dict

// Dictionary maps IDs to indexes 0, 1, 2, … in order of first appearance.
// Deleted IDs leave a tombstone: their index stays reserved, the ID is
// forgotten, and interning the ID again assigns a new index. It is not safe
// for concurrent use.
type Dictionary struct {
	index   map[string]uint32
	ids     []string
	deleted int
}

// New returns an empty dictionary.
func New() *Dictionary { return &Dictionary{index: make(map[string]uint32)} }

// FromIDs returns a dictionary with the given IDs at their positions; an
// empty string marks a deleted index. ok is false if an ID occurs twice.
func FromIDs(ids []string) (d *Dictionary, ok bool) {
	d = &Dictionary{index: make(map[string]uint32, len(ids)), ids: ids}
	for i, id := range ids {
		if id == "" {
			d.deleted++
			continue
		}
		if _, dup := d.index[id]; dup {
			return nil, false
		}
		d.index[id] = uint32(i)
	}
	return d, true
}

// Intern returns the index of id, adding it if needed. id must not be
// empty.
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

// Delete forgets id and returns its former index. ok is false if id is
// unknown.
func (d *Dictionary) Delete(id string) (uint32, bool) {
	i, ok := d.index[id]
	if !ok {
		return 0, false
	}
	delete(d.index, id)
	d.ids[i] = ""
	d.deleted++
	return i, true
}

// Deleted reports whether index i was deleted.
func (d *Dictionary) Deleted(i uint32) bool { return int(i) < len(d.ids) && d.ids[i] == "" }

// Tombstones returns the number of deleted indexes.
func (d *Dictionary) Tombstones() int { return d.deleted }

// ID returns the ID at index i, or "" if it was deleted. i must be smaller
// than Len.
func (d *Dictionary) ID(i uint32) string { return d.ids[i] }

// Len returns the number of indexes, including deleted ones.
func (d *Dictionary) Len() int { return len(d.ids) }

// IDs returns the IDs in index order, "" for deleted ones. The slice must
// not be modified.
func (d *Dictionary) IDs() []string { return d.ids }
