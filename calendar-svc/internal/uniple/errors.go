package uniple

import (
	"context"
	"errors"
	"fmt"
)

// ErrSession is the login gone: uniple refused the refresh token, and only a
// fresh login brings it back.
var ErrSession = errors.New("uniple 로그인이 풀렸습니다")

// ErrUnreachable is uniple not answering: the network, or its servers down.
var ErrUnreachable = errors.New("uniple 에 닿지 않습니다")

// Refused is uniple answering no: the HTTP status, and PostgREST's code and
// message when it sent them (42501 a row-level security refusal, 23505 a
// duplicate, 22P02 a malformed value).
type Refused struct {
	Status  int
	Code    string
	Message string
}

func (e *Refused) Error() string {
	return fmt.Sprintf("uniple 이 거부했습니다(%d %s): %s", e.Status, e.Code, e.Message)
}

// Down is a refusal that is uniple's own trouble, not the request's.
func (e *Refused) Down() bool { return e.Status >= 500 }

// Forbidden is a refusal of who asked: a row that is not theirs to touch.
func (e *Refused) Forbidden() bool {
	return e.Status == 401 || e.Status == 403 || e.Code == "42501"
}

// Missing is a refusal because the row is not there (any more).
func (e *Refused) Missing() bool { return e.Status == 404 || e.Code == "PGRST116" }

// Duplicate is a refusal because the same thing is already there.
func (e *Refused) Duplicate() bool { return e.Status == 409 || e.Code == "23505" }

// unreachable wraps a failed round trip, unless the caller gave up first.
func unreachable(ctx context.Context, what string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("%w: %s: %v", ErrUnreachable, what, err)
}
