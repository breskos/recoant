package knn_test

import (
	"context"
	"fmt"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/knn"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

// Recommend products bought together with what a user bought, and keep the
// model current with Refresh as new signals arrive.
func ExampleItemKNN() {
	s, _ := store.New(signal.DefaultModel())
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	buy := func(user string, products ...string) {
		for _, p := range products {
			_ = s.Add(recoant.Signal{
				Subject: recoant.U(user), Object: recoant.Ref{Kind: "product", ID: p},
				Type: recoant.Purchase, Value: 1, Time: now,
			})
		}
	}
	buy("anna", "tent", "sleeping-bag", "stove")
	buy("ben", "tent", "sleeping-bag")
	buy("cem", "phone", "charger")

	m, _ := knn.NewItemKNN(s, knn.Config{Relation: store.Relation{Subject: recoant.User, Object: "product"}})
	_ = m.Fit(context.Background())

	req := recoant.Request{Subject: recoant.U("ben"), Target: "product", K: 3, ExcludeSeen: true}
	res, _ := m.Recommend(context.Background(), req)
	fmt.Println("ben:", res[0].Ref.ID)

	buy("dana", "charger", "phone-case") // new product
	_ = m.Refresh(context.Background())
	for _, n := range m.Neighbors(recoant.Ref{Kind: "product", ID: "phone-case"}) {
		fmt.Println("phone-case ~", n.Ref.ID)
	}
	// Output:
	// ben: stove
	// phone-case ~ charger
}
