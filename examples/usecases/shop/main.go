// Command shop shows recoant in an online shop: a personal start page,
// "customers also bought", bestsellers, a price filter and returns that
// count against a product.
//
//	go run ./examples/usecases/shop
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/attr"
	"github.com/breskos/recoant/config"
)

//go:embed config.json
var configJSON string

type product struct {
	id, name, category string
	price              float64
}

// The catalog: four departments, six products each.
var catalog = func() []product {
	departments := map[string][]string{
		"running": {"Trail shoes", "Running socks", "GPS watch", "Rain jacket", "Head lamp", "Water bottle"},
		"kitchen": {"Chef's knife", "Cast-iron pan", "Espresso pot", "Cutting board", "Spice rack", "Stand mixer"},
		"books":   {"Sci-fi novel", "Cook book", "Travel guide", "Poetry", "Crime thriller", "Comic"},
		"garden":  {"Pruning shears", "Seed set", "Watering can", "Gloves", "Raised bed", "Lawn mower"},
	}
	prices := map[string][]float64{
		"running": {120, 12, 249, 89, 35, 19},
		"kitchen": {89, 45, 29, 25, 19, 399},
		"books":   {14, 29, 22, 12, 11, 9},
		"garden":  {24, 8, 15, 12, 149, 459},
	}
	var out []product
	for _, dep := range []string{"running", "kitchen", "books", "garden"} {
		for i, name := range departments[dep] {
			out = append(out, product{id: fmt.Sprintf("%s-%d", dep, i+1), name: name, category: dep, price: prices[dep][i]})
		}
	}
	return out
}()

func ref(id string) recoant.Ref { return recoant.Ref{Kind: "product", ID: id} }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx := context.Background()

	// 1. The configuration declares kinds, signals, models and serving.
	cfg, err := config.Load(strings.NewReader(configJSON))
	if err != nil {
		return err
	}
	engine, err := cfg.Build()
	if err != nil {
		return err
	}

	// 2. Product attributes come from the catalog, as events.
	names := map[string]string{}
	for _, p := range catalog {
		names[p.id] = p.name
		for _, ev := range []attr.Event{
			{Ref: ref(p.id), Attribute: "category", Value: attr.Cat(p.category)},
			{Ref: ref(p.id), Attribute: "price", Value: attr.Num(p.price)},
		} {
			if err := engine.Apply(ev); err != nil {
				return err
			}
		}
	}

	// 3. Signals from the shop: 300 customers, each with a favorite
	// department, browse, add to cart, buy, rate and sometimes return.
	rng := rand.New(rand.NewPCG(1, 2))
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	add := func(user, typ, product string, value float64, t time.Time) error {
		return engine.Add(recoant.Signal{Subject: recoant.U(user), Object: ref(product), Type: recoant.SignalType(typ), Value: value, Time: t})
	}
	for u := range 300 {
		user := fmt.Sprintf("customer-%d", u)
		favorite := u % 4
		for range 12 {
			dep := favorite
			if rng.Float64() < 0.2 {
				dep = rng.IntN(4)
			}
			p := catalog[dep*6+rng.IntN(6)]
			t := start.Add(time.Duration(rng.IntN(30*24)) * time.Hour)
			if err := add(user, "view", p.id, 1, t); err != nil {
				return err
			}
			if rng.Float64() < 0.5 {
				if err := add(user, "cart", p.id, 1, t.Add(30*time.Second)); err != nil {
					return err
				}
			}
			if rng.Float64() < 0.4 {
				if err := add(user, "purchase", p.id, 1, t.Add(time.Minute)); err != nil {
					return err
				}
				if err := add(user, "stars", p.id, float64(3+rng.IntN(3)), t.Add(72*time.Hour)); err != nil {
					return err
				}
			}
		}
	}

	// Mia loves running, but returned the GPS watch.
	mia := "mia"
	now := start.Add(30 * 24 * time.Hour)
	for _, s := range []struct{ typ, product string }{
		{"purchase", "running-1"}, {"stars", "running-1"}, {"purchase", "running-2"},
		{"purchase", "running-3"}, {"return", "running-3"}, {"view", "kitchen-3"},
	} {
		value := 1.0
		if s.typ == "stars" {
			value = 5
		}
		if err := add(mia, s.typ, s.product, value, now); err != nil {
			return err
		}
	}

	// 4. Train every model of the configuration.
	if err := engine.Fit(ctx); err != nil {
		return err
	}

	show := func(title string, res []recoant.Scored) {
		fmt.Printf("\n%s\n", title)
		for i, r := range res {
			p, _ := engine.Attributes().Get(r.Ref, "price")
			fmt.Printf("  %d. %-15s %6.0f €\n", i+1, names[r.Ref.ID], p.Number)
		}
	}

	fmt.Println("Mia bought trail shoes (5 stars) and running socks, returned the GPS watch and looked at an espresso pot.")

	// The personal start page: what Mia does not have yet, never the
	// returned watch.
	res, err := engine.Recommend(ctx, "for_you", recoant.U(mia), 0)
	if err != nil {
		return err
	}
	show("For you, Mia:", res)

	// The same with a filter from the request: only up to 50 €.
	budget := 50.0
	res, err = engine.Recommend(ctx, "for_you", recoant.U(mia), 0, recoant.Condition{Attribute: "price", Max: &budget})
	if err != nil {
		return err
	}
	show("For you, up to 50 €:", res)

	// On a product page: customers who bought this also bought.
	res, err = engine.Recommend(ctx, "also_bought", ref("kitchen-2"), 0)
	if err != nil {
		return err
	}
	show("Customers who bought the cast-iron pan also bought:", res)

	// A new visitor without history gets the bestsellers (fallback).
	res, err = engine.Recommend(ctx, "for_you", recoant.U("new-visitor"), 0)
	if err != nil {
		return err
	}
	show("A new visitor sees the bestsellers:", res)
	return nil
}
