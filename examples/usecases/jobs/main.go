// Command jobs shows recoant on a job board with two sides: jobs for a
// candidate (near enough and within the salary range the candidate wants)
// and candidates for a job (matching skills, shortlisted for similar jobs).
// Two relations, one per direction, live in the same configuration.
//
//	go run ./examples/usecases/jobs
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
}{{"Berlin", 52.52, 13.40}, {"Cologne", 50.94, 6.96}}

var fields = []struct {
	name   string
	titles []string
	skills []string
}{
	{"engineering", []string{"Backend developer", "Go engineer", "Platform engineer", "Data engineer", "SRE"}, []string{"go", "sql", "kubernetes"}},
	{"design", []string{"UX designer", "UI designer", "Product designer", "Design lead", "Illustrator"}, []string{"figma", "user research", "prototyping"}},
	{"sales", []string{"Account executive", "Sales manager", "Key account", "Business developer", "Sales lead"}, []string{"negotiation", "crm", "b2b"}},
}

type job struct {
	id, title, field, city string
	salary                 float64
}

func jobRef(id string) recoant.Ref { return recoant.Ref{Kind: "job", ID: id} }

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
	apply := func(evs ...attr.Event) error {
		for _, ev := range evs {
			if err := engine.Apply(ev); err != nil {
				return err
			}
		}
		return nil
	}

	// Jobs: every field in every city, with a salary.
	var jobs []job
	byID := map[string]job{}
	for ci, c := range cities {
		for _, f := range fields {
			for i, title := range f.titles {
				j := job{id: fmt.Sprintf("%s-%s-%d", strings.ToLower(c.name[:3]), f.name, i), title: title, field: f.name, city: c.name, salary: float64(55+6*i+5*ci) * 1000}
				jobs = append(jobs, j)
				byID[j.id] = j
				r := jobRef(j.id)
				if err := apply(
					attr.Event{Ref: r, Attribute: "skills", Value: attr.Tags(f.skills...)},
					attr.Event{Ref: r, Attribute: "location", Value: attr.At(c.lat, c.lon)},
					attr.Event{Ref: r, Attribute: "salary", Value: attr.Num(j.salary)},
				); err != nil {
					return err
				}
			}
		}
	}

	// Candidates: skills of one field, a city and a salary range. They
	// look at, save and apply to jobs of their field; recruiters
	// shortlist and interview candidates for their jobs.
	rng := rand.New(rand.NewPCG(11, 12))
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	add := func(subject, object recoant.Ref, typ string, t time.Time) error {
		return engine.Add(recoant.Signal{Subject: subject, Object: object, Type: recoant.SignalType(typ), Value: 1, Time: t})
	}
	people := map[string]string{}
	for u := range 200 {
		id := fmt.Sprintf("candidate-%d", u)
		ci, fi := u%len(cities), u/len(cities)%len(fields)
		people[id] = fields[fi].name
		c := cities[ci]
		if err := apply(
			attr.Event{Ref: recoant.U(id), Attribute: "skills", Value: attr.Tags(fields[fi].skills...)},
			attr.Event{Ref: recoant.U(id), Attribute: "location", Value: attr.At(c.lat, c.lon)},
			attr.Event{Ref: recoant.U(id), Attribute: "min_salary", Value: attr.Num(50000)},
			attr.Event{Ref: recoant.U(id), Attribute: "max_salary", Value: attr.Num(100000)},
		); err != nil {
			return err
		}
		for range 6 {
			j := jobs[ci*15+fi*5+rng.IntN(5)]
			t := start.Add(time.Duration(rng.IntN(40*24)) * time.Hour)
			typ := []string{"view", "save", "apply"}[rng.IntN(3)]
			if err := add(recoant.U(id), jobRef(j.id), typ, t); err != nil {
				return err
			}
			if typ == "apply" && rng.Float64() < 0.6 {
				if err := add(jobRef(j.id), recoant.U(id), "shortlist", t.Add(48*time.Hour)); err != nil {
					return err
				}
			}
		}
	}

	// Lena: Go developer in Berlin, wants 60–80k, applied to two
	// engineering jobs and dismissed a data engineering job.
	now := start.Add(45 * 24 * time.Hour)
	lena := recoant.U("lena")
	if err := apply(
		attr.Event{Ref: lena, Attribute: "skills", Value: attr.Tags("go", "sql", "kubernetes")},
		attr.Event{Ref: lena, Attribute: "location", Value: attr.At(52.50, 13.42)},
		attr.Event{Ref: lena, Attribute: "min_salary", Value: attr.Num(60000)},
		attr.Event{Ref: lena, Attribute: "max_salary", Value: attr.Num(80000)},
	); err != nil {
		return err
	}
	for _, s := range []struct{ typ, id string }{{"apply", "ber-engineering-0"}, {"apply", "ber-engineering-1"}, {"dismiss", "ber-engineering-3"}} {
		if err := add(lena, jobRef(s.id), s.typ, now); err != nil {
			return err
		}
	}

	if err := engine.Fit(ctx); err != nil {
		return err
	}

	showJobs := func(title string, res []recoant.Scored) {
		fmt.Printf("\n%s\n", title)
		for i, r := range res {
			j := byID[r.Ref.ID]
			fmt.Printf("  %d. %-20s %-12s %-8s %3.0fk €\n", i+1, j.title, j.field, j.city, j.salary/1000)
		}
	}

	fmt.Println("Lena, Go developer in Berlin, wants 60–80k, applied as backend developer and Go engineer and dismissed data engineer.")

	// Configured filters: a shared skill, within 30 km and a salary in
	// Lena's range.
	res, err := engine.Recommend(ctx, "jobs_for_you", lena, 0)
	if err != nil {
		return err
	}
	showJobs("Jobs for Lena (matching skills, within 30 km, 60–80k):", res)

	res, err = engine.Recommend(ctx, "similar_jobs", jobRef("ber-engineering-0"), 0)
	if err != nil {
		return err
	}
	showJobs("Candidates who looked at the backend developer job in Berlin also looked at:", res)

	// The other side: a recruiter asks for candidates for a job. The
	// "recruits" relation runs from job to candidate; candidates must
	// share a skill with the job and live within 30 km.
	j := byID["ber-engineering-2"]
	res, err = engine.Recommend(ctx, "candidates_for_job", jobRef(j.id), 0)
	if err != nil {
		return err
	}
	fmt.Printf("\nCandidates for %s in %s (shortlisted for similar jobs, matching skills, within 30 km):\n", j.title, j.city)
	for i, r := range res {
		fmt.Printf("  %d. %-14s %s\n", i+1, r.Ref.ID, people[r.Ref.ID])
	}
	return nil
}
