package match_test

import (
	"context"
	"fmt"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/als"
	"github.com/breskos/recoant/match"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

// A dating app: Ana, Ben, Cem and Dan like each other; Ana has not met Ben
// yet. Everyone also likes Sky, but Sky only likes Eva, Fay and Gil. One
// way, Sky is a fine suggestion for Ana; reciprocally, only Ben is.
func Example() {
	ctx := context.Background()
	s, _ := store.New(signal.DefaultModel())
	like := func(from, to string) {
		_ = s.Add(recoant.Signal{Subject: recoant.U(from), Object: recoant.U(to), Type: recoant.Purchase, Value: 1, Time: time.Unix(0, 0)})
	}
	group := []string{"ana", "ben", "cem", "dan", "eli"}
	for _, u := range group {
		for _, v := range group {
			if u != v && (u != "ana" || v != "ben") {
				like(u, v)
			}
		}
		if u != "ana" {
			like(u, "sky")
		}
	}
	for _, u := range []string{"eva", "fay", "gil"} {
		like("sky", u)
		like(u, "sky")
	}
	likes := store.Relation{Subject: recoant.User, Object: recoant.User}
	model, _ := als.New(s, als.Config{Relation: likes, Factors: 4, Iterations: 15, Seed: 1})
	_ = model.Fit(ctx)

	reciprocal, _ := match.New(s, model, model, match.Config{Relation: likes, MinEach: 0.2})
	res, _ := reciprocal.Recommend(ctx, recoant.Request{Subject: recoant.U("ana"), Target: recoant.User, K: 3, ExcludeSeen: true})
	for _, r := range res {
		fmt.Println(r.Ref.ID)
	}
	// Output:
	// ben
}
