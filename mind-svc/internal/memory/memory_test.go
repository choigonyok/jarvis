package memory

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/choigonyok/jarvis/mind-svc/internal/model"
)

// A request memory-svc turns down as it is (400, 422) says so, apart from
// one that may pass if sent again.
func TestRefusedIsToldApart(t *testing.T) {
	code := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
	defer srv.Close()
	c := NewHTTP(srv.URL, "")
	for _, tc := range []struct {
		code    int
		refused bool
	}{{http.StatusUnprocessableEntity, true}, {http.StatusBadRequest, true}, {http.StatusBadGateway, false}, {http.StatusUnauthorized, false}} {
		code = tc.code
		err := c.Records(context.Background(), []model.Record{{Key: "k"}})
		if err == nil || errors.Is(err, model.ErrRefused) != tc.refused {
			t.Errorf("%d: refused should be %v: %v", tc.code, tc.refused, err)
		}
	}
}
