package evaluation_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/popularity"
	"github.com/breskos/recoant/evaluation"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

func p(id string) recoant.Ref { return recoant.Ref{Kind: "product", ID: id} }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMetrics(t *testing.T) {
	recs := []recoant.Ref{p("a"), p("b"), p("c"), p("d")}
	rel := evaluation.NewRelevant(p("b"), p("d"), p("x"))
	if got := evaluation.Precision(recs, rel, 4); !near(got, 0.5) {
		t.Errorf("Precision = %v", got)
	}
	if got := evaluation.Precision(recs, rel, 0); got != 0 {
		t.Errorf("Precision@0 = %v", got)
	}
	if got := evaluation.Recall(recs, rel, 2); !near(got, 1.0/3) {
		t.Errorf("Recall = %v", got)
	}
	if got := evaluation.Recall(recs, nil, 2); got != 0 {
		t.Errorf("Recall empty = %v", got)
	}
	if evaluation.HitRate(recs, rel, 1) != 0 || evaluation.HitRate(recs, rel, 2) != 1 {
		t.Error("HitRate")
	}
	if got := evaluation.ReciprocalRank(recs, rel, 10); !near(got, 0.5) {
		t.Errorf("RR = %v", got)
	}
	if got := evaluation.ReciprocalRank(recs, rel, 1); got != 0 {
		t.Errorf("RR@1 = %v", got)
	}
	// DCG = 1/log2(3) + 1/log2(5); IDCG = 1 + 1/log2(3) + 1/log2(4)
	want := (1/math.Log2(3) + 1/math.Log2(5)) / (1 + 1/math.Log2(3) + 0.5)
	if got := evaluation.NDCG(recs, rel, 4); !near(got, want) {
		t.Errorf("NDCG = %v, want %v", got, want)
	}
	if got := evaluation.NDCG(recs, nil, 4); got != 0 {
		t.Errorf("NDCG empty = %v", got)
	}
	// AP = (1/2 + 2/4) / 3
	if got := evaluation.AveragePrecision(recs, rel, 4); !near(got, 1.0/3) {
		t.Errorf("AP = %v", got)
	}
	if got := evaluation.AveragePrecision(recs, nil, 4); got != 0 {
		t.Errorf("AP empty = %v", got)
	}
	perfect := []recoant.Ref{p("b"), p("d"), p("x")}
	if !near(evaluation.NDCG(perfect, rel, 3), 1) || !near(evaluation.AveragePrecision(perfect, rel, 3), 1) {
		t.Error("perfect ranking must score 1")
	}
}

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func sig(user, product string, typ recoant.SignalType, h int) recoant.Signal {
	return recoant.Signal{Subject: recoant.U(user), Object: p(product), Type: typ, Value: 1, Time: t0.Add(time.Duration(h) * time.Hour)}
}

func TestSplits(t *testing.T) {
	sigs := []recoant.Signal{
		sig("u1", "a", recoant.Visit, 0),
		sig("u1", "b", recoant.Visit, 1),
		sig("u1", "b", recoant.Purchase, 2),
		sig("u1", "a", recoant.Click, 3), // a is the last object of u1
		sig("u2", "c", recoant.Visit, 4),
	}
	train, test := evaluation.SplitByTime(sigs, t0.Add(2*time.Hour))
	if len(train) != 2 || len(test) != 3 {
		t.Errorf("SplitByTime %d/%d", len(train), len(test))
	}
	train, test, err := evaluation.SplitByFraction(sigs, 0.6)
	if err != nil || len(train) != 3 || len(test) != 2 {
		t.Errorf("SplitByFraction %d/%d %v", len(train), len(test), err)
	}
	if _, _, err := evaluation.SplitByFraction(sigs, 1); err == nil {
		t.Error("fraction 1 accepted")
	}
	if a, b, err := evaluation.SplitByFraction(nil, 0.5); a != nil || b != nil || err != nil {
		t.Error("empty input")
	}
	train, test = evaluation.LeaveLastOut(sigs, 1)
	if len(test) != 2 || test[0].Object != p("a") || test[1].Object != p("a") {
		t.Errorf("LeaveLastOut test = %v", test)
	}
	if len(train) != 3 {
		t.Errorf("LeaveLastOut train = %v", train)
	}
}

type failing struct{}

func (failing) Recommend(context.Context, recoant.Request) ([]recoant.Scored, error) {
	return nil, errors.New("boom")
}

func TestEvaluateErrors(t *testing.T) {
	ctx := context.Background()
	test := []recoant.Signal{sig("u1", "a", recoant.Visit, 0)}
	if _, err := evaluation.Evaluate(ctx, failing{}, test, evaluation.Config{}); err == nil {
		t.Error("invalid config accepted")
	}
	if _, err := evaluation.Evaluate(ctx, failing{}, test, evaluation.Config{K: 1, Target: "product"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("recommender error: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	m, _ := popularity.New(signal.DefaultModel(), popularity.Config{})
	if _, err := evaluation.Evaluate(canceled, m, test, evaluation.Config{K: 1, Target: "product"}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	rep, err := evaluation.Evaluate(ctx, m, nil, evaluation.Config{K: 1, Target: "product"})
	if err != nil || rep.Subjects != 0 {
		t.Errorf("empty test: %+v %v", rep, err)
	}
}

func TestGroundTruth(t *testing.T) {
	test := []recoant.Signal{
		sig("u1", "a", recoant.Visit, 0),
		sig("u1", "b", recoant.Purchase, 0),
		{Subject: p("a"), Object: p("b"), Type: recoant.Visit, Value: 1, Time: t0},
	}
	truth := evaluation.GroundTruth(test, evaluation.Config{Target: "product", Relevant: []recoant.SignalType{recoant.Purchase}})
	if len(truth) != 1 || len(truth[recoant.U("u1")]) != 1 {
		t.Errorf("truth = %v", truth)
	}
}

// Evaluate a popularity model on a small hand-made history: b is the most
// popular product, so users who later buy b count as hits.
func TestEvaluatePopularity(t *testing.T) {
	ctx := context.Background()
	train := []recoant.Signal{
		sig("u1", "b", recoant.Purchase, 0),
		sig("u2", "b", recoant.Purchase, 1),
		sig("u3", "a", recoant.Visit, 2),
		sig("u4", "c", recoant.Click, 3),
	}
	test := []recoant.Signal{
		sig("u3", "b", recoant.Purchase, 10), // hit at rank 1
		sig("u4", "a", recoant.Purchase, 11), // with u4 seen c excluded: a at rank 2
		sig("u5", "d", recoant.Purchase, 12), // d unknown: miss
		sig("u5", "x", recoant.Visit, 12),    // not relevant: only purchases count
	}
	s, _ := store.New(signal.DefaultModel())
	if _, err := s.AddBatch(ctx, train); err != nil {
		t.Fatal(err)
	}
	m, _ := popularity.New(signal.DefaultModel(), popularity.Config{})
	if err := m.Fit(ctx, train); err != nil {
		t.Fatal(err)
	}
	m.SetSeen(s)
	cfg := evaluation.Config{K: 2, Target: "product", Relevant: []recoant.SignalType{recoant.Purchase}, ExcludeSeen: true, Catalog: 4, Workers: 2}
	rep, err := evaluation.Evaluate(ctx, m, test, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// u3: [b c] → hit at 1; u4: [b a] → hit at 2; u5: [b c] → miss.
	if rep.Subjects != 3 || !near(rep.HitRate, 2.0/3) || !near(rep.MRR, (1+0.5)/3) || !near(rep.Precision, 1.0/3) {
		t.Errorf("report = %+v", rep)
	}
	if rep.Recommended != 3 || !near(rep.Coverage, 0.75) {
		t.Errorf("coverage = %d, %v", rep.Recommended, rep.Coverage)
	}
	var buf bytes.Buffer
	if _, err := rep.WriteTo(&buf); err != nil || !strings.Contains(buf.String(), "ndcg@2") {
		t.Errorf("WriteTo: %q %v", buf.String(), err)
	}
}

// Objects with the same last time are held out in ID order.
func TestLeaveLastOutTies(t *testing.T) {
	sigs := []recoant.Signal{sig("u1", "b", recoant.Visit, 1), sig("u1", "a", recoant.Visit, 1), sig("u1", "c", recoant.Visit, 0)}
	_, test := evaluation.LeaveLastOut(sigs, 1)
	if len(test) != 1 || test[0].Object != p("a") {
		t.Errorf("test = %v", test)
	}
}
