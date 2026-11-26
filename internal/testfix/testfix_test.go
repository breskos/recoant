package testfix

import (
	"context"
	"errors"
	"testing"

	"github.com/breskos/recoant"
)

type perfect struct{ userToUser bool }

func (p perfect) Recommend(_ context.Context, req recoant.Request) ([]recoant.Scored, error) {
	switch {
	case req.Target == recoant.User && !p.userToUser:
		return nil, errors.New("unsupported")
	case req.Target == recoant.User:
		return []recoant.Scored{{Ref: recoant.U("u2")}}, nil
	case req.Subject == recoant.U("u1"):
		return []recoant.Scored{{Ref: P("c")}}, nil
	case req.Subject == recoant.U("u4"):
		return []recoant.Scored{{Ref: P("z")}}, nil
	}
	return []recoant.Scored{{Ref: P("b")}}, nil
}

func TestFixture(t *testing.T) {
	s := Store(t, Groups)
	if s.Stats().Signals != int64(len(Groups)) {
		t.Fatal("Store")
	}
	CheckGroups(t, perfect{userToUser: true}, true)
	CheckGroups(t, perfect{}, false)
	if got := IDs([]recoant.Scored{{Ref: P("a")}}); len(got) != 1 || got[0] != "a" {
		t.Fatal("IDs")
	}
}

type respectsRejected struct{}

func (respectsRejected) Recommend(_ context.Context, req recoant.Request) ([]recoant.Scored, error) {
	if req.IncludeRejected {
		return []recoant.Scored{{Ref: P("c")}}, nil
	}
	return []recoant.Scored{{Ref: P("b")}}, nil
}

func TestNegative(t *testing.T) {
	s := NegativeStore(t)
	if !s.Rejected(recoant.U("u1"), P("c")) {
		t.Fatal("u1 must reject c")
	}
	if len(Hidden()) != len(Groups)+1 {
		t.Fatal("Hidden")
	}
	CheckRejected(t, respectsRejected{})
}

func TestCheckCancel(t *testing.T) {
	calls := 0
	CheckCancel(t, func(ctx context.Context) error {
		calls++
		for range 3 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		return nil
	})
	if calls != 4 {
		t.Errorf("%d calls, want 4", calls)
	}
}
