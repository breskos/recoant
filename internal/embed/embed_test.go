package embed

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/breskos/recoant/internal/topk"
)

func TestMatrix(t *testing.T) {
	m := &Matrix{N: 3, K: 2, Data: []float64{1, 0, 0, 2, 3, 4}}
	if n := m.Norms(); n[0] != 1 || n[1] != 2 || n[2] != 5 {
		t.Fatalf("Norms = %v", n)
	}
	all := func(uint32) bool { return true }
	tk := topk.New(2)
	m.Score([]float64{1, 1}, []float64{0, 0, -10}, all, tk)
	if got := tk.Sorted(); got[0].Index != 1 || got[1].Index != 0 {
		t.Errorf("Score = %v", got)
	}
	tk = topk.New(3)
	m.Similar([]float64{0, 1}, 1, m.Norms(), func(j uint32) bool { return j != 1 }, tk)
	got := tk.Sorted()
	if len(got) != 2 || got[0].Index != 2 || math.Abs(got[0].Score-0.8) > 1e-12 {
		t.Errorf("Similar = %v", got)
	}
	tk = topk.New(3)
	m.Similar([]float64{0, 0}, 0, m.Norms(), all, tk)
	zero := New(1, 2)
	zero.Similar([]float64{1, 0}, 1, zero.Norms(), all, tk)
	if len(tk.Sorted()) != 0 {
		t.Error("zero vectors must be skipped")
	}
	rng := rand.New(rand.NewPCG(1, 1))
	r := Random(rng, 2, 3, 0.1)
	r.Grow(4, rng, 0.1)
	if r.N != 4 || len(r.Data) != 12 || r.Row(3)[2] == 0 {
		t.Errorf("Grow: %+v", r)
	}
}

func TestScoreAdmit(t *testing.T) {
	m := &Matrix{N: 2, K: 1, Data: []float64{1, 2}}
	tk := topk.New(2)
	m.Score([]float64{1}, nil, func(j uint32) bool { return j == 0 }, tk)
	if got := tk.Sorted(); len(got) != 1 || got[0].Index != 0 {
		t.Errorf("Score with admit = %v", got)
	}
}
