// Command customers shows recoant segments in a shop: who are my
// customers, what does each group buy, how does one customer compare, and
// where does a new customer belong.
//
//	go run ./examples/usecases/customers
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/attr"
	"github.com/breskos/recoant/config"
)

//go:embed config.json
var configJSON string

var products = map[string][]string{
	"games":       {"Racing game", "Puzzle game", "Strategy game", "Party game", "Indie game"},
	"electronics": {"Headset", "Keyboard", "Webcam", "Power bank", "Smart watch"},
	"garden":      {"Seeds", "Watering can", "Rose shears", "Garden gloves", "Bird house"},
	"household":   {"Kettle", "Vacuum", "Laundry basket", "Lunch boxes", "First aid kit"},
}

var categories = []string{"games", "electronics", "garden", "household"}

// The hidden groups: two buy games and electronics, two garden and
// household; age tells them apart.
var groups = []struct {
	name   string
	cats   []int
	ageMin int
	ageMax int
}{
	{"students", []int{0, 1}, 18, 25},
	{"techies", []int{0, 1}, 32, 45},
	{"families", []int{2, 3}, 30, 42},
	{"retirees", []int{2, 3}, 63, 80},
}

func ref(id string) recoant.Ref { return recoant.Ref{Kind: "product", ID: id} }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()
	cfg, err := config.Load(strings.NewReader(configJSON))
	if err != nil {
		return err
	}
	engine, err := cfg.Build()
	if err != nil {
		return err
	}
	names := map[string]string{}
	for c, cat := range categories {
		for i, name := range products[cat] {
			id := fmt.Sprintf("%s-%d", cat, i)
			names[id] = name
			if err := engine.Apply(attr.Event{Ref: ref(id), Attribute: "category", Value: attr.Cat(categories[c])}); err != nil {
				return err
			}
		}
	}

	// 400 customers: age, city, membership and what they view and buy.
	rng := rand.New(rand.NewPCG(4, 2))
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cities := []string{"Berlin", "Hamburg", "Munich"}
	add := func(user, typ, product string, t time.Time) error {
		return engine.Add(recoant.Signal{Subject: recoant.U(user), Object: ref(product), Type: recoant.SignalType(typ), Value: 1, Time: t})
	}
	customer := func(id string, age int) error {
		membership := "basic"
		if rng.Float64() < 0.3 {
			membership = "premium"
		}
		for _, ev := range []attr.Event{
			{Ref: recoant.U(id), Attribute: "age", Value: attr.Num(float64(age))},
			{Ref: recoant.U(id), Attribute: "city", Value: attr.Cat(cities[rng.IntN(len(cities))])},
			{Ref: recoant.U(id), Attribute: "membership", Value: attr.Cat(membership)},
		} {
			if err := engine.Apply(ev); err != nil {
				return err
			}
		}
		return nil
	}
	for u := range 400 {
		id, g := fmt.Sprintf("c%d", u), u%len(groups)
		gr := groups[g]
		if err := customer(id, gr.ageMin+rng.IntN(gr.ageMax-gr.ageMin+1)); err != nil {
			return err
		}
		for range 8 + rng.IntN(8) {
			cat := gr.cats[rng.IntN(2)]
			if rng.Float64() < 0.15 {
				cat = rng.IntN(len(categories))
			}
			p := fmt.Sprintf("%s-%d", categories[cat], rng.IntN(5))
			t := start.Add(time.Duration(rng.IntN(60*24)) * time.Hour)
			typ := "view"
			if rng.Float64() < 0.4 {
				typ = "purchase"
			}
			if err := add(id, typ, p, t); err != nil {
				return err
			}
		}
	}
	// Tom, 38, buys games and a headset and returned a keyboard.
	if err := customer("tom", 38); err != nil {
		return err
	}
	now := start.Add(61 * 24 * time.Hour)
	for _, s := range []struct{ typ, p string }{{"purchase", "games-0"}, {"purchase", "games-2"}, {"purchase", "electronics-0"}, {"return", "electronics-1"}} {
		if err := add("tom", s.typ, s.p, now); err != nil {
			return err
		}
	}

	// Training also fits the segments and stores each customer's segment
	// in the attribute "segment".
	if err := engine.Fit(ctx); err != nil {
		return err
	}
	seg, _ := engine.Segmenter(recoant.User)

	fmt.Println("Who are my customers? 4 segments from what they buy and their age:")
	for _, p := range seg.Profiles() {
		cats := map[string]bool{}
		for _, x := range p.Typical {
			cats[strings.Split(x.Ref.ID, "-")[0]] = true
		}
		var top []string
		for c := range cats {
			top = append(top, c)
		}
		slices.Sort(top)
		age := p.Attributes["age"]
		premium := 0.0
		for _, v := range p.Attributes["membership"].Values {
			if v.Value == "premium" {
				premium = v.Share
			}
		}
		fmt.Printf("  s%d  %3d customers (%2.0f %%)  age %2.0f–%2.0f  buys %-22s  %.1f products each  premium %2.0f %%\n",
			p.Segment, p.Size, 100*p.Share, age.Min, age.Max, strings.Join(top, ", "), p.Activity, 100*premium)
	}

	// One customer, assessed.
	prof, _ := seg.Profile(recoant.U("tom"), 3)
	var near []string
	for _, x := range prof.Nearest {
		near = append(near, x.Ref.ID)
	}
	typical := "typical"
	if prof.Distance > 1.5 {
		typical = "unusual"
	}
	fmt.Printf("\nTom, 38: segment s%d, %s for it (distance %.2f), %d products: %d liked, %d returned; most similar: %s\n",
		prof.Segment, typical, prof.Distance, prof.Interactions, prof.Positive, prof.Negative, strings.Join(near, ", "))

	res, err := engine.Recommend(ctx, "for_you", recoant.U("tom"), 0)
	if err != nil {
		return err
	}
	var recs []string
	for _, r := range res {
		recs = append(recs, names[r.Ref.ID])
	}
	fmt.Printf("Recommended for Tom: %s\n", strings.Join(recs, ", "))

	// Segments as a filter: other customers from Tom's segment.
	res, err = engine.Recommend(ctx, "people_like_me", recoant.U("tom"), 0)
	if err != nil {
		return err
	}
	var peers []string
	for _, r := range res {
		v, _ := engine.Attributes().Get(r.Ref, "segment")
		peers = append(peers, r.Ref.ID+" ("+v.Category+")")
	}
	fmt.Printf("Customers like Tom: %s\n", strings.Join(peers, ", "))

	// A new customer with one view is placed by age and that view.
	if err := customer("lea", 21); err != nil {
		return err
	}
	if err := add("lea", "view", "games-1", now); err != nil {
		return err
	}
	if err := engine.Refresh(ctx); err != nil {
		return err
	}
	j, _ := seg.Segment(recoant.U("lea"))
	fmt.Printf("\nNew customer Lea, 21, viewed a puzzle game: segment s%d\n", j)
	return nil
}
