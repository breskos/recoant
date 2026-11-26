package attr_test

import (
	"context"
	"fmt"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/attr"
)

// candidates is a stand-in for any recommender: it returns all users.
type candidates []string

func (c candidates) Recommend(_ context.Context, req recoant.Request) ([]recoant.Scored, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	var out []recoant.Scored
	for _, id := range c {
		if ref := recoant.U(id); ref != req.Subject && (req.Filter == nil || req.Filter(ref)) {
			out = append(out, recoant.Scored{Ref: ref, Score: 1})
		}
	}
	return out, nil
}

// Mutual age preferences and a distance limit for people discovery: both
// sides' preferences must be met.
func Example_matching() {
	s, err := attr.New(attr.Schema{recoant.User: {
		"age":      {Type: attr.Number, Sensitive: true},
		"min_age":  {Type: attr.Number},
		"max_age":  {Type: attr.Number},
		"location": {Type: attr.Geo, Sensitive: true},
	}})
	if err != nil {
		panic(err)
	}
	for _, u := range []struct {
		id                  string
		age, minAge, maxAge float64
		lat, lon            float64
	}{
		{"ann", 30, 28, 40, 52.52, 13.40}, // Berlin
		{"bob", 35, 25, 32, 52.50, 13.45}, // Berlin, likes ann's age
		{"cem", 33, 35, 45, 52.51, 13.39}, // Berlin, wants someone older than ann
		{"dan", 34, 25, 35, 48.14, 11.58}, // Munich, too far
	} {
		for _, ev := range []attr.Event{
			{Ref: recoant.U(u.id), Attribute: "age", Value: attr.Num(u.age)},
			{Ref: recoant.U(u.id), Attribute: "min_age", Value: attr.Num(u.minAge)},
			{Ref: recoant.U(u.id), Attribute: "max_age", Value: attr.Num(u.maxAge)},
			{Ref: recoant.U(u.id), Attribute: "location", Value: attr.At(u.lat, u.lon)},
		} {
			if err := s.Apply(ev); err != nil {
				panic(err)
			}
		}
	}
	rec := attr.Filtered(candidates{"ann", "bob", "cem", "dan"}, s)
	res, err := rec.Recommend(context.Background(), recoant.Request{
		Subject: recoant.U("ann"), Target: recoant.User, K: 10,
		Where: []recoant.Condition{
			{Attribute: "age", Within: []string{"min_age", "max_age"}, Mutual: true},
			{Attribute: "location", MaxDistanceKm: 50},
		},
	})
	if err != nil {
		panic(err)
	}
	for _, r := range res {
		fmt.Println(r.Ref.ID)
	}
	// Sensitive attributes stay out of profiles unless asked for.
	fmt.Println(len(s.Attributes(recoant.U("ann"), false)))
	// Output:
	// bob
	// 2
}
