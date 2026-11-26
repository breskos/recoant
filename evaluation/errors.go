package evaluation

import (
	"math"
	"slices"

	"github.com/breskos/recoant"
)

// Predictor predicts the value of a subject–object pair, e.g. a rating.
type Predictor interface {
	Predict(subject, object recoant.Ref) (float64, bool)
}

// ErrorReport holds prediction errors for explicit feedback.
type ErrorReport struct {
	N       int     `json:"n"`       // predicted signals
	Missing int     `json:"missing"` // signals the predictor could not predict
	RMSE    float64 `json:"rmse"`
	MAE     float64 `json:"mae"`
}

// Errors compares the predictions of p with the values of the test signals
// of the given types (all types if empty).
func Errors(p Predictor, test []recoant.Signal, types []recoant.SignalType) ErrorReport {
	var r ErrorReport
	var sq, abs float64
	for _, s := range test {
		if len(types) > 0 && !slices.Contains(types, s.Type) {
			continue
		}
		pred, ok := p.Predict(s.Subject, s.Object)
		if !ok {
			r.Missing++
			continue
		}
		d := pred - s.Value
		sq += d * d
		abs += math.Abs(d)
		r.N++
	}
	if r.N > 0 {
		r.RMSE = math.Sqrt(sq / float64(r.N))
		r.MAE = abs / float64(r.N)
	}
	return r
}
