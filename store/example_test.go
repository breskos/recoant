package store_test

import (
	"fmt"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

// Feed signals into a store and read the strongest interactions of a user.
func Example() {
	s, err := store.New(signal.DefaultModel())
	if err != nil {
		panic(err)
	}
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	add := func(product string, typ recoant.SignalType, value float64) {
		_ = s.Add(recoant.Signal{
			Subject: recoant.U("u-17"),
			Object:  recoant.Ref{Kind: "product", ID: product},
			Type:    typ, Value: value, Time: now,
		})
	}
	add("lamp", recoant.Visit, 1)
	add("lamp", recoant.Dwell, 120)
	add("sofa", recoant.Visit, 1)
	add("sofa", recoant.Purchase, 1)
	add("rug", recoant.Visit, 1)

	for _, x := range s.Interactions(recoant.U("u-17"), "product", now) {
		fmt.Printf("%-5s %.2f\n", x.Ref.ID, x.Score)
	}
	// Output:
	// sofa  11.00
	// lamp  3.59
	// rug   1.00
}
