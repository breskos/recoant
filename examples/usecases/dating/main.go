// Command dating shows recoant in a dating or people-discovery app: people
// who match your taste, only if both are in each other's age range and
// nearby, never again someone you passed, and an account deletion that
// removes a person everywhere.
//
//	go run ./examples/usecases/dating
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

var cities = []struct {
	name     string
	lat, lon float64
}{{"Berlin", 52.52, 13.40}, {"Hamburg", 53.55, 9.99}, {"Munich", 48.14, 11.58}}

var scenes = [][]string{
	{"hiking", "climbing", "cycling"},
	{"theatre", "painting", "jazz"},
	{"board games", "video games", "anime"},
	{"cooking", "wine", "street food"},
}

var firstNames = []string{"Alex", "Robin", "Sam", "Kim", "Charlie", "Jo", "Luca", "Noa", "Mika", "Toni", "Ari", "Pat", "Eli", "Jules", "Kai", "Sascha"}

type person struct {
	id, name, city string
	age, minAge    float64
	maxAge         float64
	scene          int
	lat, lon       float64
}

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

	// 1. Profiles: age, the age range someone is looking for, location
	// and interests, set as attribute events.
	rng := rand.New(rand.NewPCG(7, 7))
	var people []person
	for i := range 400 {
		c := cities[i%len(cities)]
		age := float64(22 + rng.IntN(20))
		p := person{
			id: fmt.Sprintf("p%d", i), name: firstNames[i%len(firstNames)], city: c.name,
			age: age, minAge: age - 4, maxAge: age + 5, scene: rng.IntN(len(scenes)),
			lat: c.lat + rng.NormFloat64()*0.05, lon: c.lon + rng.NormFloat64()*0.05,
		}
		people = append(people, p)
	}
	mia := person{id: "mia", name: "Mia", city: "Berlin", age: 29, minAge: 27, maxAge: 35, scene: 0, lat: 52.50, lon: 13.42}
	people = append(people, mia)
	byID := map[string]person{}
	for _, p := range people {
		byID[p.id] = p
		u := recoant.U(p.id)
		for _, ev := range []attr.Event{
			{Ref: u, Attribute: "age", Value: attr.Num(p.age)},
			{Ref: u, Attribute: "min_age", Value: attr.Num(p.minAge)},
			{Ref: u, Attribute: "max_age", Value: attr.Num(p.maxAge)},
			{Ref: u, Attribute: "location", Value: attr.At(p.lat, p.lon)},
			{Ref: u, Attribute: "interests", Value: attr.Tags(scenes[p.scene]...)},
		} {
			if err := engine.Apply(ev); err != nil {
				return err
			}
		}
	}

	// 2. Swipes: people like profiles from their own scene and pass on
	// most others.
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	swipe := func(from, to, typ string, t time.Time) error {
		return engine.Add(recoant.Signal{Subject: recoant.U(from), Object: recoant.U(to), Type: recoant.SignalType(typ), Value: 1, Time: t})
	}
	for _, p := range people[:400] {
		for range 25 {
			q := people[rng.IntN(400)]
			if q.id == p.id {
				continue
			}
			t := start.Add(time.Duration(rng.IntN(50*24)) * time.Hour)
			typ := "pass"
			if q.scene == p.scene && rng.Float64() < 0.8 || rng.Float64() < 0.1 {
				typ = "like"
			}
			if err := swipe(p.id, q.id, typ, t); err != nil {
				return err
			}
		}
	}

	// Mia liked three outdoor people and passed on someone.
	now := start.Add(55 * 24 * time.Hour)
	liked, passed := 0, ""
	for _, p := range people[:400] {
		switch {
		case p.scene == 0 && liked < 3:
			liked++
			if err := swipe("mia", p.id, "like", now); err != nil {
				return err
			}
		case p.scene == 0 && passed == "" && p.city == "Berlin":
			passed = p.id
			if err := swipe("mia", p.id, "pass", now); err != nil {
				return err
			}
		}
	}

	// People see Mia too: outdoor people mostly like her, others mostly pass.
	for _, p := range people[:400] {
		if rng.Float64() > 0.3 {
			continue
		}
		typ := "pass"
		if p.scene == mia.scene && rng.Float64() < 0.8 || rng.Float64() < 0.1 {
			typ = "like"
		}
		if err := swipe(p.id, "mia", typ, now.Add(-time.Duration(rng.IntN(20*24))*time.Hour)); err != nil {
			return err
		}
	}

	if err := engine.Fit(ctx); err != nil {
		return err
	}

	show := func(title string, res []recoant.Scored) {
		fmt.Printf("\n%s\n", title)
		for i, r := range res {
			p := byID[r.Ref.ID]
			km := attr.DistanceKm(attr.Point{Lat: mia.lat, Lon: mia.lon}, attr.Point{Lat: p.lat, Lon: p.lon})
			fmt.Printf("  %d. %-7s %2.0f, %-7s %4.0f km  looks for %2.0f–%2.0f  likes %s\n",
				i+1, p.name, p.age, p.city, km, p.minAge, p.maxAge, strings.Join(scenes[p.scene], ", "))
		}
	}

	fmt.Printf("Mia, 29, Berlin, looks for 27–35, liked three outdoor people and passed on %s (%s).\n", byID[passed].name, passed)

	// The discovery feed: taste from the swipes, filtered by mutual age
	// ranges and 50 km — both people must fit each other's range.
	res, err := engine.Recommend(ctx, "discover", recoant.U("mia"), 0)
	if err != nil {
		return err
	}
	show("Discover for Mia (mutual age range, within 50 km):", res)
	for _, r := range res {
		if r.Ref.ID == passed {
			return fmt.Errorf("passed profile %s recommended", passed)
		}
	}

	// A request can narrow it further, e.g. "only hikers".
	res, err = engine.Recommend(ctx, "discover", recoant.U("mia"), 3, recoant.Condition{Attribute: "interests", In: []string{"hiking"}})
	if err != nil {
		return err
	}
	show("Only people who like hiking:", res)

	// Account deletion: the first suggestion deletes the account. It is
	// gone from every feed at once, with its attributes.
	if len(res) == 0 {
		return fmt.Errorf("no hikers for Mia")
	}
	gone := res[0].Ref
	engine.Delete(gone)
	res, err = engine.Recommend(ctx, "discover", recoant.U("mia"), 3, recoant.Condition{Attribute: "interests", In: []string{"hiking"}})
	if err != nil {
		return err
	}
	show(fmt.Sprintf("After %s (%s) deleted the account:", byID[gone.ID].name, gone.ID), res)

	// Privacy: an export leaves sensitive attributes (age, location) out.
	fmt.Printf("\nExport of Mia's profile without sensitive attributes: %v\n", keys(engine.ExportAttributes(recoant.U("mia"))))

	// Retention: likes older than 365 days are dropped.
	fmt.Printf("Retention a year later drops %d old swipes.\n", engine.Expire(now.AddDate(1, 0, 1)))
	return nil
}

func keys(m map[string]attr.Value) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
