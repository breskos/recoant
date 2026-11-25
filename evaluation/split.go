package evaluation

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/breskos/recoant"
)

// SplitByTime puts signals before cutoff into train and the others into
// test, keeping their order. It is the most realistic split: the model only
// knows the past.
func SplitByTime(sigs []recoant.Signal, cutoff time.Time) (train, test []recoant.Signal) {
	for _, s := range sigs {
		if s.Time.Before(cutoff) {
			train = append(train, s)
		} else {
			test = append(test, s)
		}
	}
	return train, test
}

// SplitByFraction splits at the time below which the given fraction of
// signals lies. Signals at the same time stay on the same side.
func SplitByFraction(sigs []recoant.Signal, fraction float64) (train, test []recoant.Signal, err error) {
	if !(fraction > 0 && fraction < 1) {
		return nil, nil, fmt.Errorf("evaluation: fraction must be in (0, 1), got %v", fraction)
	}
	if len(sigs) == 0 {
		return nil, nil, nil
	}
	times := make([]time.Time, len(sigs))
	for i, s := range sigs {
		times[i] = s.Time
	}
	slices.SortFunc(times, time.Time.Compare)
	cutoff := times[min(int(fraction*float64(len(times))), len(times)-1)]
	train, test = SplitByTime(sigs, cutoff)
	return train, test, nil
}

// LeaveLastOut holds out, per subject, the n objects the subject interacted
// with last. All signals between the subject and these objects go to test,
// so no part of a held-out interaction leaks into training. Subjects with n
// or fewer objects stay in train completely.
func LeaveLastOut(sigs []recoant.Signal, n int) (train, test []recoant.Signal) {
	type pair struct{ subject, object recoant.Ref }
	last := make(map[pair]time.Time)
	for _, s := range sigs {
		k := pair{s.Subject, s.Object}
		if t, ok := last[k]; !ok || s.Time.After(t) {
			last[k] = s.Time
		}
	}
	type objTime struct {
		object recoant.Ref
		t      time.Time
	}
	perSubject := make(map[recoant.Ref][]objTime)
	for k, t := range last {
		perSubject[k.subject] = append(perSubject[k.subject], objTime{k.object, t})
	}
	held := make(map[pair]struct{})
	for subject, objs := range perSubject {
		if len(objs) <= n {
			continue
		}
		slices.SortFunc(objs, func(a, b objTime) int {
			if c := b.t.Compare(a.t); c != 0 {
				return c
			}
			return cmp.Compare(a.object.ID, b.object.ID)
		})
		for _, o := range objs[:n] {
			held[pair{subject, o.object}] = struct{}{}
		}
	}
	for _, s := range sigs {
		if _, ok := held[pair{s.Subject, s.Object}]; ok {
			test = append(test, s)
		} else {
			train = append(train, s)
		}
	}
	return train, test
}
