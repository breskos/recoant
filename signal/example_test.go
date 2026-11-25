package signal_test

import (
	"fmt"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/signal"
)

// A long dwell time counts more than a short one, and a week-old purchase
// counts less than a fresh one.
func Example() {
	m := signal.DefaultModel()
	m.HalfLife = 7 * 24 * time.Hour
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	short := recoant.Signal{Type: recoant.Dwell, Value: 5, Time: now}
	long := recoant.Signal{Type: recoant.Dwell, Value: 300, Time: now}
	old := recoant.Signal{Type: recoant.Purchase, Value: 1, Time: now.Add(-7 * 24 * time.Hour)}

	fmt.Printf("dwell 5s:      %.2f\n", m.Strength(short, now))
	fmt.Printf("dwell 300s:    %.2f\n", m.Strength(long, now))
	fmt.Printf("old purchase:  %.2f\n", m.Strength(old, now))
	// Output:
	// dwell 5s:      0.24
	// dwell 300s:    2.98
	// old purchase:  5.00
}
