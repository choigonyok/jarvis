// Package collect is what the message collectors (kakaotalk-client,
// imessage-client) have in common: the bearer check in front of their
// read-only surface, and the retention that keeps their plaintext copies
// from growing forever.
package collect

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/choigonyok/jarvis/gocommon/svc"
)

// Guard requires "Authorization: Bearer <token>". An empty token lets
// everything through (a local trial; the collectors refuse to start that way
// unless told to).
func Guard(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" && !svc.Bearer(r, token) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

// Retain calls prune with "now minus days" (unix seconds) once a day until ctx
// ends. days <= 0 keeps everything. report hears how many were deleted, or
// the error.
func Retain(ctx context.Context, days int, prune func(before int64) (int64, error), report func(n int64, err error)) {
	if days <= 0 {
		return
	}
	for {
		n, err := prune(time.Now().AddDate(0, 0, -days).Unix())
		if report != nil && (err != nil || n > 0) {
			report(n, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}
