// Command events shows recoant on an events and meetup platform: events in
// your own city that match your interests, events near where you are right
// now, a free-only filter and past events that disappear once they are
// over.
//
//	go run ./examples/usecases/events
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

var cities = []struct {
	name     string
	lat, lon float64
}{{"Hamburg", 53.55, 9.99}, {"Berlin", 52.52, 13.40}, {"Munich", 48.14, 11.58}}

var topics = map[string][]string{
	"tech":    {"Go meetup", "AI night", "Hackathon", "Startup pitch", "Cloud summit"},
	"music":   {"Jazz session", "Open mic", "Techno night", "Choir concert", "Indie gig"},
	"sports":  {"Park run", "Climbing meetup", "Yoga in the park", "Bike tour", "Beach volleyball"},
	"culture": {"Museum night", "Poetry slam", "Film club", "Book club", "Theatre premiere"},
}

var topicOrder = []string{"tech", "music", "sports", "culture"}

type event struct {
	id, name, topic, city string
	lat, lon, price       float64
}

func ref(id string) recoant.Ref { return recoant.Ref{Kind: "event", ID: id} }

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

	// Every city has every event, some free, some paid.
	var events []event
	byID := map[string]event{}
	for ci, c := range cities {
		for ti, topic := range topicOrder {
			for i, name := range topics[topic] {
				e := event{
					id: fmt.Sprintf("%s-%s-%d", strings.ToLower(c.name[:3]), topic, i), name: name, topic: topic, city: c.name,
					lat: c.lat + float64(i)*0.01, lon: c.lon + float64(ti)*0.01, price: float64((ci + ti + i) % 3 * 15),
				}
				events = append(events, e)
				byID[e.id] = e
				for _, ev := range []attr.Event{
					{Ref: ref(e.id), Attribute: "topic", Value: attr.Cat(e.topic)},
					{Ref: ref(e.id), Attribute: "city", Value: attr.Cat(e.city)},
					{Ref: ref(e.id), Attribute: "location", Value: attr.At(e.lat, e.lon)},
					{Ref: ref(e.id), Attribute: "price", Value: attr.Num(e.price)},
				} {
					if err := engine.Apply(ev); err != nil {
						return err
					}
				}
			}
		}
	}

	// Members live in a city and mostly go to events of one topic there.
	rng := rand.New(rand.NewPCG(9, 9))
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	add := func(user, typ, id string, t time.Time) error {
		return engine.Add(recoant.Signal{Subject: recoant.U(user), Object: ref(id), Type: recoant.SignalType(typ), Value: 1, Time: t})
	}
	perCity := len(events) / len(cities)
	for u := range 300 {
		user := fmt.Sprintf("member-%d", u)
		city, topic := u%len(cities), u/len(cities)%len(topicOrder)
		if err := engine.Apply(attr.Event{Ref: recoant.U(user), Attribute: "city", Value: attr.Cat(cities[city].name)}); err != nil {
			return err
		}
		for range 8 {
			c, t := city, topic
			if rng.Float64() < 0.2 {
				t = rng.IntN(len(topicOrder))
			}
			if rng.Float64() < 0.1 {
				c = rng.IntN(len(cities))
			}
			e := events[c*perCity+t*5+rng.IntN(5)]
			typ := "interested"
			if rng.Float64() < 0.5 {
				typ = "rsvp"
			}
			if err := add(user, typ, e.id, start.Add(time.Duration(rng.IntN(50*24))*time.Hour)); err != nil {
				return err
			}
		}
	}

	// Ben lives in Hamburg and goes to tech events.
	now := start.Add(56 * 24 * time.Hour)
	if err := engine.Apply(attr.Event{Ref: recoant.U("ben"), Attribute: "city", Value: attr.Cat("Hamburg")}); err != nil {
		return err
	}
	for _, id := range []string{"ham-tech-0", "ham-tech-1"} {
		if err := add("ben", "rsvp", id, now); err != nil {
			return err
		}
	}
	if err := add("ben", "not_interested", "ham-music-2", now); err != nil {
		return err
	}

	if err := engine.Fit(ctx); err != nil {
		return err
	}

	show := func(title string, res []recoant.Scored) {
		fmt.Printf("\n%s\n", title)
		for i, r := range res {
			e := byID[r.Ref.ID]
			price := "free"
			if e.price > 0 {
				price = fmt.Sprintf("%.0f €", e.price)
			}
			fmt.Printf("  %d. %-18s %-8s %-8s %s\n", i+1, e.name, e.topic, e.city, price)
		}
	}

	fmt.Println("Ben lives in Hamburg, signed up for the Go meetup and AI night and is not interested in techno nights.")

	// "in_my_city" compares the event's city with Ben's city ("same").
	res, err := engine.Recommend(ctx, "in_my_city", recoant.U("ben"), 0)
	if err != nil {
		return err
	}
	show("Events for Ben in his city:", res)

	// Ben travels to Munich: the app sends where he is right now.
	munich := [2]float64{cities[2].lat, cities[2].lon}
	near := recoant.Condition{Attribute: "location", Near: &munich, MaxDistanceKm: 10}
	res, err = engine.Recommend(ctx, "anywhere", recoant.U("ben"), 0, near)
	if err != nil {
		return err
	}
	show("Ben is in Munich this weekend (within 10 km):", res)

	free := 0.0
	res, err = engine.Recommend(ctx, "anywhere", recoant.U("ben"), 0, near, recoant.Condition{Attribute: "price", Max: &free})
	if err != nil {
		return err
	}
	show("… and only free ones:", res)

	// When an event is over, delete it: it disappears from all
	// recommendations at once, before the next training.
	over := res[0].Ref
	engine.Delete(over)
	res, err = engine.Recommend(ctx, "anywhere", recoant.U("ben"), 0, near, recoant.Condition{Attribute: "price", Max: &free})
	if err != nil {
		return err
	}
	show(fmt.Sprintf("After the %s is over:", byID[over.ID].name), res)
	return nil
}
