package recoant_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/breskos/recoant"
)

func TestRef(t *testing.T) {
	r := recoant.U("42")
	if r.String() != "user:42" {
		t.Fatalf("String() = %q", r.String())
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []recoant.Ref{{ID: "1"}, {Kind: "product"}} {
		if err := bad.Validate(); !errors.Is(err, recoant.ErrInvalidRef) {
			t.Errorf("Validate(%v) = %v", bad, err)
		}
	}
}

func TestSignalValidate(t *testing.T) {
	ok := recoant.Signal{
		Subject: recoant.U("u"),
		Object:  recoant.Ref{Kind: "product", ID: "p"},
		Type:    recoant.Click,
		Value:   1,
		Time:    time.Unix(1, 0),
	}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(s *recoant.Signal){
		"subject": func(s *recoant.Signal) { s.Subject.ID = "" },
		"object":  func(s *recoant.Signal) { s.Object.Kind = "" },
		"type":    func(s *recoant.Signal) { s.Type = "" },
		"nan":     func(s *recoant.Signal) { s.Value = math.NaN() },
		"inf":     func(s *recoant.Signal) { s.Value = math.Inf(1) },
		"time":    func(s *recoant.Signal) { s.Time = time.Time{} },
	}
	for name, mutate := range cases {
		s := ok
		mutate(&s)
		if err := s.Validate(); !errors.Is(err, recoant.ErrInvalidSignal) {
			t.Errorf("%s: Validate() = %v", name, err)
		}
	}
}

func TestRequestValidate(t *testing.T) {
	ok := recoant.Request{Subject: recoant.U("u"), Target: "product", K: 10}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(r *recoant.Request){
		"subject": func(r *recoant.Request) { r.Subject = recoant.Ref{} },
		"target":  func(r *recoant.Request) { r.Target = "" },
		"k":       func(r *recoant.Request) { r.K = 0 },
	}
	for name, mutate := range cases {
		r := ok
		mutate(&r)
		if err := r.Validate(); !errors.Is(err, recoant.ErrInvalidRequest) {
			t.Errorf("%s: Validate() = %v", name, err)
		}
	}
}
