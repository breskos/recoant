package persist_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/popularity"
	"github.com/breskos/recoant/persist"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

func TestFormatString(t *testing.T) {
	if persist.JSON.String() != "json" || persist.Binary.String() != "binary" || persist.Format(9).String() != "Format(9)" {
		t.Error("String")
	}
}

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var sigs []recoant.Signal
	for i, typ := range []recoant.SignalType{recoant.Visit, recoant.Click, recoant.Dwell, recoant.Cart, recoant.Purchase, recoant.Visit, recoant.Click} {
		for u := range 3 {
			sigs = append(sigs, recoant.Signal{
				Subject: recoant.U(fmt.Sprint("u", u)), Object: recoant.Ref{Kind: "item", ID: fmt.Sprint("i", (i+u)%6)},
				Type: typ, Value: 30, Time: t0.Add(time.Duration(i*3+u) * time.Hour),
			})
		}
	}
	s, _ := store.New(signal.DefaultModel())
	if _, err := s.AddBatch(ctx, sigs); err != nil {
		t.Fatal(err)
	}
	pop, _ := popularity.New(signal.DefaultModel(), popularity.Config{HalfLife: 24 * time.Hour})
	if err := pop.Fit(ctx, sigs); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	user := recoant.U("u1")
	now := s.Stats().Latest
	for _, f := range []persist.Format{persist.JSON, persist.Binary} {
		sp := filepath.Join(dir, "store."+f.String())
		if err := persist.SaveStore(sp, f, s); err != nil {
			t.Fatal(err)
		}
		s2, err := persist.LoadStore(sp)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		a, b := s.Interactions(user, "item", now), s2.Interactions(user, "item", now)
		if len(a) == 0 || len(a) != len(b) || a[0] != b[0] {
			t.Errorf("%s: store differs", f)
		}

		mp := filepath.Join(dir, "model."+f.String())
		if err := persist.SaveSignalModel(mp, f, s.Model()); err != nil {
			t.Fatal(err)
		}
		m, err := persist.LoadSignalModel(mp)
		if err != nil || m.HalfLife != s.Model().HalfLife || len(m.Rules) != len(s.Model().Rules) {
			t.Errorf("%s: signal model %+v %v", f, m, err)
		}

		pp := filepath.Join(dir, "pop."+f.String())
		if err := persist.SavePopularity(pp, f, pop); err != nil {
			t.Fatal(err)
		}
		pop2, err := persist.LoadPopularity(pp)
		if err != nil {
			t.Fatal(err)
		}
		req := recoant.Request{Subject: user, Target: "item", K: 5}
		x, _ := pop.Recommend(ctx, req)
		y, _ := pop2.Recommend(ctx, req)
		if len(x) != 5 || x[0] != y[0] || x[4] != y[4] {
			t.Errorf("%s: popularity differs", f)
		}
	}
	js, _ := os.ReadFile(filepath.Join(dir, "store.json"))
	if !bytes.HasPrefix(js, []byte(`{"recoant":1,"kind":"store"`)) {
		t.Errorf("JSON envelope: %.40s", js)
	}
	bin, _ := os.ReadFile(filepath.Join(dir, "store.binary"))
	if !bytes.HasPrefix(bin, []byte("RCNT")) {
		t.Errorf("binary header: %.4q", bin)
	}
}

func TestErrors(t *testing.T) {
	var buf bytes.Buffer
	if err := persist.Encode(&buf, persist.Format(7), "x", 1); !errors.Is(err, persist.ErrFormat) {
		t.Errorf("unknown format: %v", err)
	}
	if err := persist.Encode(&buf, persist.Binary, strings.Repeat("k", 300), 1); err == nil {
		t.Error("long kind accepted")
	}
	if err := persist.Encode(&buf, persist.JSON, "x", func() {}); err == nil {
		t.Error("unencodable value accepted")
	}
	var v int
	for name, c := range map[string]struct {
		in   string
		kind string
		want error
	}{
		"empty":          {"", "x", persist.ErrFormat},
		"garbage":        {"hello world", "x", persist.ErrFormat},
		"json version":   {`{"recoant":9,"kind":"x","data":1}`, "x", persist.ErrVersion},
		"json kind":      {`{"recoant":1,"kind":"y","data":1}`, "x", persist.ErrKind},
		"binary short":   {"RCNT", "x", persist.ErrFormat},
		"binary version": {"RCNT\x09\x01x", "x", persist.ErrVersion},
		"binary kind":    {"RCNT\x01\x01y", "x", persist.ErrKind},
		"binary kindlen": {"RCNT\x01\x05x", "x", persist.ErrFormat},
		"binary data":    {"RCNT\x01\x01xzz", "x", persist.ErrFormat},
	} {
		if _, err := persist.Decode(strings.NewReader(c.in), c.kind, &v); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	if _, err := persist.LoadStore(missing); err == nil {
		t.Error("LoadStore missing")
	}
	if _, err := persist.LoadSignalModel(missing); err == nil {
		t.Error("LoadSignalModel missing")
	}
	if _, err := persist.LoadPopularity(missing); err == nil {
		t.Error("LoadPopularity missing")
	}
	if err := persist.Save(filepath.Join(dir, "no", "such", "dir"), persist.JSON, "x", 1); err == nil {
		t.Error("Save into missing dir")
	}
	bad := filepath.Join(dir, "bad")
	if err := persist.Save(bad, persist.Format(7), "x", 1); err == nil {
		t.Error("Save with unknown format")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Error("failed Save left a file")
	}
}

func ExampleSaveStore() {
	s, _ := store.New(signal.DefaultModel())
	_ = s.Add(recoant.Signal{
		Subject: recoant.U("u1"), Object: recoant.Ref{Kind: "product", ID: "p1"},
		Type: recoant.Purchase, Value: 1, Time: time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC),
	})
	dir, _ := os.MkdirTemp("", "recoant")
	defer func() { _ = os.RemoveAll(dir) }()

	path := filepath.Join(dir, "store.bin")
	if err := persist.SaveStore(path, persist.Binary, s); err != nil {
		panic(err)
	}
	loaded, err := persist.LoadStore(path)
	if err != nil {
		panic(err)
	}
	fmt.Println(loaded.Stats().Interactions)
	// Output: 1
}
