package fdecay

import (
	"math"
	"testing"
	"time"
)

func TestClock(t *testing.T) {
	hl := time.Hour
	c := Clock{Rate: math.Ln2 / hl.Seconds()}
	t0 := time.Unix(1000, 0)
	if c.At(t0) != 1 {
		t.Fatal("At before epoch must be 1")
	}
	stored := 4 * c.Scale(t0)          // 4 at t0
	stored += 2 * c.Scale(t0.Add(hl))  // 2 at t0+1h
	got := stored * c.At(t0.Add(2*hl)) // at t0+2h: 4/4 + 2/2 = 2
	if math.Abs(got-2) > 1e-9 {
		t.Fatalf("decayed sum = %v", got)
	}
	far := t0.Add(100 * hl)
	if !c.NeedsRebase(far) {
		t.Fatal("expected rebase")
	}
	f := c.Rebase(t0.Add(2 * hl))
	if math.Abs(stored*f-2) > 1e-9 || !c.Epoch.Equal(t0.Add(2*hl)) {
		t.Fatalf("rebase factor %v", f)
	}
	none := Clock{}
	if none.Scale(t0) != 1 || none.At(far) != 1 || none.NeedsRebase(far) {
		t.Fatal("zero rate must not decay")
	}
}
