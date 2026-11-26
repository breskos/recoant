// Command music shows recoant in a music or podcast app: a weekly mix that
// follows changing taste, skips that count against a track, a radio that
// plays similar tracks, charts and a family-friendly filter.
//
//	go run ./examples/usecases/music
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

// Fictional artists, eight tracks per genre.
var genres = map[string][]string{
	"jazz":      {"Blue Lantern", "Miles Apart", "Velvet Trio", "Night Owls"},
	"techno":    {"Kraftwerkstatt", "Pulse Unit", "Neon Grid", "Subframe"},
	"indie":     {"Paper Kites", "The Lowlands", "Golden Hour", "Sea Glass"},
	"hip-hop":   {"MC Parallax", "Lyric Theory", "Block Party", "Northside"},
	"classical": {"Aurora Quartet", "Philharmonie Nord", "Cello Lines", "Etude"},
}

var order = []string{"jazz", "techno", "indie", "hip-hop", "classical"}

type track struct{ id, title, genre string }

var tracks = func() []track {
	var out []track
	for _, g := range order {
		for i, artist := range genres[g] {
			for n := range 2 {
				out = append(out, track{id: fmt.Sprintf("%s-%d-%d", g, i, n), title: fmt.Sprintf("%s – Track %c", artist, 'A'+n), genre: g})
			}
		}
	}
	return out
}()

func ref(id string) recoant.Ref { return recoant.Ref{Kind: "track", ID: id} }

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

	titles := map[string]track{}
	for i, t := range tracks {
		titles[t.id] = t
		explicit := "no"
		if t.genre == "hip-hop" && i%2 == 0 {
			explicit = "yes"
		}
		for _, ev := range []attr.Event{
			{Ref: ref(t.id), Attribute: "genre", Value: attr.Cat(t.genre)},
			{Ref: ref(t.id), Attribute: "explicit", Value: attr.Cat(explicit)},
		} {
			if err := engine.Apply(ev); err != nil {
				return err
			}
		}
	}

	// Listeners: each has a favorite genre. A play carries the seconds
	// listened; the signal model saturates it, so a full play counts more
	// than a ten-second preview, but not endlessly more.
	rng := rand.New(rand.NewPCG(3, 4))
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	add := func(user, typ, id string, value float64, t time.Time) error {
		return engine.Add(recoant.Signal{Subject: recoant.U(user), Object: ref(id), Type: recoant.SignalType(typ), Value: value, Time: t})
	}
	for u := range 300 {
		user := fmt.Sprintf("listener-%d", u)
		g := u % len(order)
		for range 15 {
			pick := g
			if rng.Float64() < 0.15 {
				pick = rng.IntN(len(order))
			}
			t := tracks[pick*8+rng.IntN(8)]
			when := now.Add(-time.Duration(rng.IntN(60*24)) * time.Hour)
			if err := add(user, "play", t.id, float64(30+rng.IntN(200)), when); err != nil {
				return err
			}
			if rng.Float64() < 0.2 {
				if err := add(user, "like", t.id, 1, when); err != nil {
					return err
				}
			}
		}
	}

	// Leo listened to jazz all spring, but in the last two weeks only to
	// techno, and skipped one techno track after a few seconds.
	for i := range 3 {
		if err := add("leo", "play", tracks[i].id, 240, now.AddDate(0, -6, i)); err != nil {
			return err
		}
	}
	for i := range 4 {
		if err := add("leo", "play", tracks[8+i].id, 240, now.AddDate(0, 0, -i-1)); err != nil {
			return err
		}
	}
	if err := add("leo", "skip", tracks[12].id, 1, now.AddDate(0, 0, -1)); err != nil {
		return err
	}

	if err := engine.Fit(ctx); err != nil {
		return err
	}

	show := func(title string, res []recoant.Scored) {
		fmt.Printf("\n%s\n", title)
		for i, r := range res {
			t := titles[r.Ref.ID]
			fmt.Printf("  %d. %-34s %s\n", i+1, t.title, t.genre)
		}
	}

	fmt.Printf("Leo played three jazz tracks half a year ago, four techno tracks this week and skipped %q.\n", titles[tracks[12].id].title)

	// Old plays fade with the half-life of 21 days, so the mix follows
	// Leo's current taste.
	res, err := engine.Recommend(ctx, "weekly_mix", recoant.U("leo"), 0)
	if err != nil {
		return err
	}
	show("Leo's weekly mix:", res)

	// Track radio: tracks listened to by the same people.
	res, err = engine.Recommend(ctx, "track_radio", ref("indie-0-0"), 0)
	if err != nil {
		return err
	}
	show(fmt.Sprintf("Radio after %q:", titles["indie-0-0"].title), res)

	// Charts, and the same in family mode: no explicit tracks.
	res, err = engine.Recommend(ctx, "charts", recoant.U("guest"), 0)
	if err != nil {
		return err
	}
	show("Charts of the last days:", res)
	res, err = engine.Recommend(ctx, "charts", recoant.U("guest"), 0, recoant.Condition{Attribute: "explicit", In: []string{"no"}})
	if err != nil {
		return err
	}
	show("Charts in family mode (no explicit tracks):", res)
	return nil
}
