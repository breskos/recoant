// Command recipes shows recoant in a recipe app: star ratings around a
// neutral point (one star counts against a recipe), "ready in 30 minutes"
// as a configured filter, "vegetarian" as a filter from the request and
// similar recipes.
//
//	go run ./examples/usecases/recipes
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

type recipe struct {
	id, name, cuisine string
	diet              []string
	minutes           float64
}

var cuisines = []string{"italian", "asian", "mexican", "german"}

var recipes = []recipe{
	{"it-1", "Lasagne", "italian", nil, 90},
	{"it-2", "Pasta al pomodoro", "italian", []string{"vegetarian", "vegan"}, 20},
	{"it-3", "Risotto ai funghi", "italian", []string{"vegetarian"}, 40},
	{"it-4", "Pizza margherita", "italian", []string{"vegetarian"}, 30},
	{"it-5", "Saltimbocca", "italian", nil, 25},
	{"it-6", "Minestrone", "italian", []string{"vegetarian", "vegan"}, 45},
	{"as-1", "Pad thai", "asian", nil, 25},
	{"as-2", "Vegetable curry", "asian", []string{"vegetarian", "vegan"}, 35},
	{"as-3", "Ramen", "asian", nil, 120},
	{"as-4", "Fried rice", "asian", []string{"vegetarian"}, 15},
	{"as-5", "Sushi bowl", "asian", nil, 30},
	{"as-6", "Tofu stir-fry", "asian", []string{"vegetarian", "vegan"}, 20},
	{"mx-1", "Tacos al pastor", "mexican", nil, 60},
	{"mx-2", "Bean burrito", "mexican", []string{"vegetarian"}, 20},
	{"mx-3", "Guacamole", "mexican", []string{"vegetarian", "vegan"}, 10},
	{"mx-4", "Enchiladas", "mexican", nil, 50},
	{"mx-5", "Quesadilla", "mexican", []string{"vegetarian"}, 15},
	{"mx-6", "Chili sin carne", "mexican", []string{"vegetarian", "vegan"}, 40},
	{"de-1", "Schnitzel", "german", nil, 30},
	{"de-2", "Käsespätzle", "german", []string{"vegetarian"}, 35},
	{"de-3", "Potato salad", "german", []string{"vegetarian", "vegan"}, 25},
	{"de-4", "Goulash", "german", nil, 150},
	{"de-5", "Apple strudel", "german", []string{"vegetarian"}, 70},
	{"de-6", "Flammkuchen", "german", nil, 25},
}

func ref(id string) recoant.Ref { return recoant.Ref{Kind: "recipe", ID: id} }

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

	byID := map[string]recipe{}
	for _, r := range recipes {
		byID[r.id] = r
		evs := []attr.Event{
			{Ref: ref(r.id), Attribute: "cuisine", Value: attr.Cat(r.cuisine)},
			{Ref: ref(r.id), Attribute: "minutes", Value: attr.Num(r.minutes)},
		}
		if len(r.diet) > 0 {
			evs = append(evs, attr.Event{Ref: ref(r.id), Attribute: "diet", Value: attr.Tags(r.diet...)})
		}
		for _, ev := range evs {
			if err := engine.Apply(ev); err != nil {
				return err
			}
		}
	}

	// Home cooks open, save and cook recipes of their favorite cuisine and
	// rate them; recipes of other cuisines get worse ratings.
	rng := rand.New(rand.NewPCG(5, 6))
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	add := func(user, typ, id string, value float64, t time.Time) error {
		return engine.Add(recoant.Signal{Subject: recoant.U(user), Object: ref(id), Type: recoant.SignalType(typ), Value: value, Time: t})
	}
	for u := range 250 {
		user := fmt.Sprintf("cook-%d", u)
		fav := u % len(cuisines)
		for range 10 {
			c, stars := fav, 4+rng.IntN(2)
			if rng.Float64() < 0.25 {
				c, stars = rng.IntN(len(cuisines)), 1+rng.IntN(3)
			}
			r := recipes[c*6+rng.IntN(6)]
			t := start.Add(time.Duration(rng.IntN(80*24)) * time.Hour)
			if err := add(user, "open", r.id, 1, t); err != nil {
				return err
			}
			if rng.Float64() < 0.5 {
				if err := add(user, "cooked", r.id, 1, t.Add(time.Hour)); err != nil {
					return err
				}
				if err := add(user, "stars", r.id, float64(stars), t.Add(2*time.Hour)); err != nil {
					return err
				}
			}
		}
	}

	// Anna loves Asian food — and tried a schnitzel, which she rated with
	// one star: below the neutral point of three, it counts against it.
	now := start.Add(85 * 24 * time.Hour)
	for _, s := range []struct {
		id    string
		stars float64
	}{{"as-1", 5}, {"as-3", 5}, {"as-5", 4}, {"de-1", 1}} {
		if err := add("anna", "cooked", s.id, 1, now); err != nil {
			return err
		}
		if err := add("anna", "stars", s.id, s.stars, now); err != nil {
			return err
		}
	}

	if err := engine.Fit(ctx); err != nil {
		return err
	}

	show := func(title string, res []recoant.Scored) {
		fmt.Printf("\n%s\n", title)
		for i, s := range res {
			r := byID[s.Ref.ID]
			fmt.Printf("  %d. %-18s %-8s %3.0f min  %s\n", i+1, r.name, r.cuisine, r.minutes, strings.Join(r.diet, ", "))
		}
	}

	fmt.Println("Anna rated pad thai and ramen with 5 stars, the sushi bowl with 4 and the schnitzel with 1.")

	res, err := engine.Recommend(ctx, "for_you", recoant.U("anna"), 0)
	if err != nil {
		return err
	}
	show("Recipes for Anna:", res)

	// "tonight" is configured with a filter: ready in 30 minutes.
	res, err = engine.Recommend(ctx, "tonight", recoant.U("anna"), 0)
	if err != nil {
		return err
	}
	show("Tonight, ready in 30 minutes:", res)

	// The app adds the diet from Anna's settings to the request.
	res, err = engine.Recommend(ctx, "tonight", recoant.U("anna"), 0, recoant.Condition{Attribute: "diet", In: []string{"vegetarian"}})
	if err != nil {
		return err
	}
	show("Tonight, vegetarian:", res)

	res, err = engine.Recommend(ctx, "similar", ref("mx-3"), 0)
	if err != nil {
		return err
	}
	show("Cooks who made guacamole also made:", res)
	return nil
}
