package store

import (
	"cmp"
	"slices"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/sparse"
)

// Delete removes a reference and everything recorded about it: its rows as
// subject and its columns as object in all relations. Its dense index
// stays reserved (a tombstone) so that the indexes of models remain valid,
// but the ID is forgotten; adding a signal with the same ID later creates a
// new index. The affected rows and columns count as changed (see
// [Store.Changes]). Delete reports whether the reference was known.
func (s *Store) Delete(ref recoant.Ref) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.dicts[ref.Kind]
	if d == nil {
		return false
	}
	i, ok := d.Lookup(ref.ID)
	if !ok {
		return false
	}
	s.seq++
	for key, r := range s.rels {
		if key.Subject == ref.Kind {
			s.dropRow(r, i)
		}
		if key.Object == ref.Kind {
			s.dropCol(r, i)
		}
	}
	d.Delete(ref.ID)
	s.markDead(ref.Kind, i)
	return true
}

func (s *Store) markDead(kind recoant.Kind, i uint32) {
	bits := s.dead[kind]
	if w := int(i / 64); w >= len(bits) {
		bits = append(bits, make([]uint64, w+1-len(bits))...)
	}
	bits[i/64] |= 1 << (i % 64)
	s.dead[kind] = bits
}

// dropRow removes subject i's row from all channels of r.
func (s *Store) dropRow(r *relation, i uint32) {
	for _, ch := range r.channels {
		row := ch.row(i)
		if len(row) == 0 {
			continue
		}
		for _, e := range row {
			r.objSeq = setSeq(r.objSeq, e.Col, s.seq)
		}
		ch.nnz -= len(row)
		ch.rows[i] = nil
		if int(i) < len(ch.counts) {
			ch.counts[i] = nil
		}
		r.subjSeq = setSeq(r.subjSeq, i, s.seq)
	}
	if int(i) < len(r.stamps) {
		r.stamps[i] = nil
	}
}

// dropCol removes object j from every row of all channels of r.
func (s *Store) dropCol(r *relation, j uint32) {
	for _, ch := range r.channels {
		for si, row := range ch.rows {
			k, ok := findCol(row, j)
			if !ok {
				continue
			}
			ch.rows[si] = slices.Delete(row, k, k+1)
			if si < len(ch.counts) {
				if k, ok := findCol(ch.counts[si], j); ok {
					ch.counts[si] = slices.Delete(ch.counts[si], k, k+1)
				}
			}
			ch.nnz--
			r.subjSeq = setSeq(r.subjSeq, uint32(si), s.seq)
			r.objSeq = setSeq(r.objSeq, j, s.seq)
		}
	}
	for si, row := range r.stamps {
		if k, ok := slices.BinarySearchFunc(row, j, func(e stamp, c uint32) int { return cmp.Compare(e.Col, c) }); ok {
			r.stamps[si] = slices.Delete(row, k, k+1)
		}
	}
}

func findCol(row []sparse.Entry, col uint32) (int, bool) {
	return slices.BinarySearchFunc(row, col, func(e sparse.Entry, c uint32) int { return cmp.Compare(e.Col, c) })
}

// Deleted returns a function that reports whether a dense index of a kind
// belongs to a deleted reference, or nil if no reference of the kind was
// deleted. The function works on a copy and is safe to call without locks.
func (s *Store) Deleted(kind recoant.Kind) func(uint32) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bits := s.dead[kind]
	if len(bits) == 0 {
		return nil
	}
	bits = slices.Clone(bits)
	return func(i uint32) bool {
		w := int(i / 64)
		return w < len(bits) && bits[w]&(1<<(i%64)) != 0
	}
}

// Expire drops the pairs of relations with a retention (see
// [signal.RelationSpec.Retention]) whose last signal is older than the
// retention at time now (zero: the latest signal), in all channels. The
// affected rows and columns count as changed. It returns the number of
// pairs dropped.
func (s *Store) Expire(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		now = s.latest
	}
	dropped := 0
	changed := false
	for _, r := range s.rels {
		if r.retention <= 0 {
			continue
		}
		cutoff := unixStamp(now.Add(-r.retention))
		for si, row := range r.stamps {
			var expired []uint32
			kept := row[:0]
			for _, st := range row {
				if st.T < cutoff {
					expired = append(expired, st.Col)
					continue
				}
				kept = append(kept, st)
			}
			if len(expired) == 0 {
				continue
			}
			if !changed {
				s.seq++
				changed = true
			}
			r.stamps[si] = kept
			dropped += len(expired)
			s.dropPairs(r, uint32(si), expired)
		}
	}
	return dropped
}

// dropPairs removes the sorted columns cols from subject i's row in all
// channels of r.
func (s *Store) dropPairs(r *relation, i uint32, cols []uint32) {
	remove := func(row []sparse.Entry) []sparse.Entry {
		return slices.DeleteFunc(row, func(e sparse.Entry) bool {
			_, found := slices.BinarySearch(cols, e.Col)
			return found
		})
	}
	for _, ch := range r.channels {
		row := ch.row(i)
		if len(row) == 0 {
			continue
		}
		before := len(row)
		ch.rows[i] = remove(row)
		ch.nnz -= before - len(ch.rows[i])
		if int(i) < len(ch.counts) {
			ch.counts[i] = remove(ch.counts[i])
		}
	}
	r.subjSeq = setSeq(r.subjSeq, i, s.seq)
	for _, c := range cols {
		r.objSeq = setSeq(r.objSeq, c, s.seq)
	}
}
