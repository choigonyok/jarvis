// Package svc is what every service's main does the same way: read its
// settings from the environment, refuse to start without what it cannot run
// without, guard its routes with the services' token, answer in JSON, and
// listen until docker stop - then let what is in flight finish.
//
// A service with a database opens it with svc/pg, kept apart so that the
// services without one do not build pgx in.
package svc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

// Logger is how a service logs: text lines on standard output.
func Logger() *slog.Logger { return slog.New(slog.NewTextHandler(os.Stdout, nil)) }

// Signals is a context that ends on SIGINT or SIGTERM (docker stop): what a
// service runs under, its loops and its server alike.
func Signals() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// exit stops the process; a test watches it instead.
var exit = os.Exit

// Env is key's value, or def when it is unset or empty.
func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Int is key as a whole number: def when it is unset, not one, or below
// zero - no count or number of days is negative.
func Int(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n >= 0 {
		return n
	}
	return def
}

// Float is key as a number: def when it is unset or not one.
func Float(key string, def float64) float64 {
	if f, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return f
	}
	return def
}

// Duration is key as a duration ("90s", "2m"): def when it is unset, not
// one, or below zero.
func Duration(key string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(key)); err == nil && d >= 0 {
		return d
	}
	return def
}

// Need is key's value, which the service cannot run without: an empty one
// stops it here, saying which.
func Need(log *slog.Logger, key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Error("꼭 있어야 하는 설정이 비어 있습니다", "env", key)
		exit(1)
	}
	return v
}

// Token is the services' bearer token, API_TOKEN. An empty one would leave
// every route open to anything on the network, so it stops the service -
// unless API_TOKEN_OPTIONAL=true says this is a local trial.
func Token(log *slog.Logger) string {
	t := os.Getenv("API_TOKEN")
	if t == "" && os.Getenv("API_TOKEN_OPTIONAL") != "true" {
		log.Error("API_TOKEN 이 비어 있습니다. 로컬 시험이라면 API_TOKEN_OPTIONAL=true")
		exit(1)
	}
	return t
}

// Bearer is whether r carries "Authorization: Bearer <token>", compared in
// constant time. An empty token matches nothing.
func Bearer(r *http.Request, token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
}

// Guard lets a request through with the services' token, and every request
// when there is none (a local trial: Token refuses to start that way unless
// told to). Anything else is 401 {"error": "인증이 필요합니다."}.
func Guard(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token != "" && !Bearer(r, token) {
			Error(w, http.StatusUnauthorized, "인증이 필요합니다.")
			return
		}
		next(w, r)
	}
}

// WriteJSON answers code with v as JSON, never cached: what a service says
// is as of now.
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Error answers code with {"error": msg}, the shape every service refuses in.
func Error(w http.ResponseWriter, code int, msg string) {
	WriteJSON(w, code, map[string]string{"error": msg})
}

// Server is how a service listens.
type Server struct {
	// Name is the service's, as the log says it is listening.
	Name    string
	Addr    string
	Handler http.Handler
	// Read is how long a request may take to come in, its body included.
	Read time.Duration
	// Write is how long a response may take to go out, counted from when
	// its request came in. A handler that waits on something as long moves
	// its own deadline (Extend); an event stream lifts it (Stream).
	Write time.Duration
	// Drain is how long requests in flight get to finish once the service
	// is told to stop: 5 seconds when zero.
	Drain time.Duration
	Log   *slog.Logger
	// Says is what else the log says as it starts listening.
	Says []any
}

// listen opens the address; a test takes the listener from it.
var listen = net.Listen

// Serve listens until ctx ends, then stops taking requests and gives those
// in flight up to Drain to finish. It returns nil once the server is down,
// and the error when it cannot listen at all: the service should exit then
// rather than carry on unreachable.
func Serve(ctx context.Context, s Server) error {
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	addr := s.Addr
	if addr == "" {
		addr = ":http"
	}
	ln, err := listen("tcp", addr)
	if err != nil {
		log.Error("HTTP 서버를 열지 못했습니다", "addr", addr, "err", err)
		return err
	}
	srv := &http.Server{Handler: s.Handler, ReadTimeout: s.Read, WriteTimeout: s.Write}
	log.Info(s.Name+" 가 듣기 시작했습니다", append([]any{"addr", addr}, s.Says...)...)
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	select {
	case err := <-served:
		log.Error("HTTP 서버", "err", err)
		return err
	case <-ctx.Done():
	}
	log.Info(s.Name + " 를 내립니다")
	drain := s.Drain
	if drain == 0 {
		drain = 5 * time.Second
	}
	down, cancel := context.WithTimeout(context.Background(), drain)
	defer cancel()
	if err := srv.Shutdown(down); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		log.Warn("HTTP 서버를 다 내리지 못했습니다", "err", err)
	}
	return nil
}

// Stream opens w for a response that lasts as long as the client stays (an
// event stream), and is the controller to flush it with. The server's write
// limit covers a whole response, so it is lifted here. The read limit needs
// nothing: net/http lifts it once the request's body is read - before the
// handler, for a request without one - and only then watches for the client
// going away, which ends the request's context.
func Stream(w http.ResponseWriter) *http.ResponseController {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	return rc
}

// Extend gives w until d from now to go out, past the server's write limit:
// a handler that waits on an answer as long as that limit still gets its
// reply out, instead of losing it to a connection the server has given up on.
func Extend(w http.ResponseWriter, d time.Duration) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d))
}
