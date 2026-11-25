package knn

import (
	"cmp"
	"context"
	"math"
	"runtime"
	"slices"
	"sync"

	"github.com/breskos/recoant/sparse"
)

// center subtracts each row's mean from its values (in place).
func center(m *sparse.CSR) {
	for i := range m.Rows {
		_, vals := m.Row(i)
		if len(vals) == 0 {
			continue
		}
		var sum float64
		for _, v := range vals {
			sum += float64(v)
		}
		mean := float32(sum / float64(len(vals)))
		for k := range vals {
			vals[k] -= mean
		}
	}
}

// similarities computes similarities between the rows of a. at must be the
// transpose of a. For each requested row, fn receives all positive
// similarities to other rows, unsorted. fn is called from several
// goroutines, at most once per row.
func similarities(ctx context.Context, a, at *sparse.CSR, rows []uint32, cfg Config, fn func(i uint32, sims []Neighbor)) error {
	norms := make([]float64, a.Rows)
	sizes := make([]int, a.Rows)
	for i := range a.Rows {
		_, vals := a.Row(i)
		var s float64
		for _, v := range vals {
			s += float64(v) * float64(v)
		}
		norms[i] = math.Sqrt(s)
		sizes[i] = len(vals)
	}
	workers := cmp.Or(cfg.Workers, runtime.NumCPU())
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	next := make(chan uint32)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			acc := make([]float64, a.Rows)
			cnt := make([]int32, a.Rows)
			var touched []uint32
			var sims []Neighbor
			for i := range next {
				touched = touched[:0]
				idx, vals := a.Row(int(i))
				for k, f := range idx {
					fidx, fvals := at.Row(int(f))
					if cfg.MaxDegree > 0 && len(fidx) > cfg.MaxDegree {
						continue
					}
					for l, j := range fidx {
						if cnt[j] == 0 {
							touched = append(touched, j)
						}
						acc[j] += float64(vals[k]) * float64(fvals[l])
						cnt[j]++
					}
				}
				sims = sims[:0]
				for _, j := range touched {
					if j != i {
						var s float64
						switch cfg.Similarity {
						case Jaccard:
							c := float64(cnt[j])
							s = c / (float64(sizes[i]+sizes[j]) - c + cfg.Shrink)
						default:
							s = acc[j] / (norms[i]*norms[j] + cfg.Shrink)
						}
						if s > 0 && !math.IsInf(s, 0) {
							sims = append(sims, Neighbor{Index: j, Sim: float32(s)})
						}
					}
					acc[j], cnt[j] = 0, 0
				}
				fn(i, sims)
			}
		}()
	}
feed:
	for _, i := range rows {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	return ctx.Err()
}

func byBest(a, b Neighbor) int {
	if c := cmp.Compare(b.Sim, a.Sim); c != 0 {
		return c
	}
	return cmp.Compare(a.Index, b.Index)
}

// top returns a sorted copy of the k most similar neighbors.
func top(sims []Neighbor, k int) []Neighbor {
	out := slices.Clone(sims)
	slices.SortFunc(out, byBest)
	return out[:min(k, len(out))]
}

// insert adds n to a sorted list of at most k neighbors that does not
// contain n.Index yet.
func insert(list []Neighbor, n Neighbor, k int) []Neighbor {
	if len(list) >= k && byBest(n, list[len(list)-1]) >= 0 {
		return list
	}
	pos, _ := slices.BinarySearchFunc(list, n, byBest)
	list = slices.Insert(list, pos, n)
	return list[:min(k, len(list))]
}

// allRows returns 0..n-1.
func allRows(n int) []uint32 {
	r := make([]uint32, n)
	for i := range r {
		r[i] = uint32(i)
	}
	return r
}

// fitTable computes the top-k neighbors of every row of a.
func fitTable(ctx context.Context, a, at *sparse.CSR, cfg Config) ([][]Neighbor, error) {
	table := make([][]Neighbor, a.Rows)
	err := similarities(ctx, a, at, allRows(a.Rows), cfg, func(i uint32, sims []Neighbor) {
		table[i] = top(sims, cfg.Neighbors)
	})
	return table, err
}

// fullSims computes all positive similarities of the changed rows.
func fullSims(ctx context.Context, a, at *sparse.CSR, changed []uint32, cfg Config) (map[uint32][]Neighbor, error) {
	full := make(map[uint32][]Neighbor, len(changed))
	var mu sync.Mutex
	err := similarities(ctx, a, at, changed, cfg, func(i uint32, sims []Neighbor) {
		c := slices.Clone(sims)
		mu.Lock()
		full[i] = c
		mu.Unlock()
	})
	return full, err
}

// applySims replaces the lists of the changed rows with their top
// neighbors and updates their entries in the lists of the other rows. The
// table grows to rows.
//
// All lists are first cleared of the changed rows in one sequential pass.
// The weakest similarity of every full list is kept in a compact array, so
// most candidates are rejected without touching their list.
func applySims(table [][]Neighbor, rows int, full map[uint32][]Neighbor, cfg Config) [][]Neighbor {
	if len(table) < rows {
		table = append(table, make([][]Neighbor, rows-len(table))...)
	}
	changed := make([]bool, rows)
	for i := range full {
		changed[i] = true
	}
	floor := make([]float32, rows) // candidates below the floor cannot enter
	for j, list := range table {
		if changed[j] {
			continue
		}
		list = slices.DeleteFunc(list, func(x Neighbor) bool { return changed[x.Index] })
		table[j] = list
		floor[j] = float32(math.Inf(-1))
		if len(list) >= cfg.Neighbors && len(list) > 0 {
			floor[j] = list[len(list)-1].Sim
		}
	}
	for i, sims := range full {
		table[i] = top(sims, cfg.Neighbors)
		for _, n := range sims {
			j := n.Index
			if changed[j] || n.Sim < floor[j] {
				continue
			}
			list := insert(table[j], Neighbor{Index: i, Sim: n.Sim}, cfg.Neighbors)
			table[j] = list
			if len(list) >= cfg.Neighbors {
				floor[j] = list[len(list)-1].Sim
			}
		}
	}
	return table
}
