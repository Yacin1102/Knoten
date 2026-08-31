package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"knoten/internal/protocol"
	"knoten/internal/store"
)

type Config struct {
	Store        *store.Store
	JoinToken    string
	SyncInterval time.Duration
	PeerTimeout  time.Duration
	TrustProxy   bool
	Logger       *log.Logger
}

type Server struct {
	cfg       Config
	startedAt time.Time
}

func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("server needs a database")
	}
	if cfg.Logger == nil {
		return nil, errors.New("server needs a logger")
	}
	if cfg.SyncInterval <= 0 {
		return nil, fmt.Errorf("sync interval must be positive, got %s", cfg.SyncInterval)
	}
	if cfg.PeerTimeout <= cfg.SyncInterval {
		return nil, fmt.Errorf(
			"peer timeout (%s) must be longer than the sync interval (%s), otherwise one missed poll drops a healthy machine out of every peer list",
			cfg.PeerTimeout, cfg.SyncInterval)
	}
	return &Server{cfg: cfg, startedAt: time.Now()}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST "+protocol.PathRegister, s.handleRegister)
	mux.HandleFunc("POST "+protocol.PathSync, s.handleSync)
	mux.HandleFunc("GET "+protocol.PathHealth, s.handleHealth)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no endpoint at %s %s", r.Method, r.URL.Path))
	})

	return recoverPanic(s.cfg.Logger, logRequests(s.cfg.Logger, limitBody(mux)))
}

const maxBodyBytes = 64 * 1024

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func logRequests(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		logger.Printf("%s %s -> %d (%d bytes) in %s from %s",
			r.Method, r.URL.Path, rec.status, rec.bytes,
			time.Since(start).Round(time.Microsecond),
			r.RemoteAddr)
	})
}

func recoverPanic(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}

				logger.Printf("PANIC while handling %s %s: %v", r.Method, r.URL.Path, rec)

				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func observedIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
		// Returning the whole RemoteAddr unmodified may break the configs and give wrong address.
		// Because later the port will be added to the address. So, we have to investigate this in the future.
	}
	return host
}

func (s *Server) checkToken(presented string) bool {
	if s.cfg.JoinToken == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(s.cfg.JoinToken)) == 1
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)

	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body is larger than %d bytes", maxBodyBytes))
			return false
		}
		writeError(w, http.StatusBadRequest, "could not read the JSON body: "+err.Error())
		return false
	}

	if dec.More() {
		writeError(w, http.StatusBadRequest, "request body contains more than one JSON object")
		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, src any) {
	body, err := json.Marshal(src)
	if err != nil {
		http.Error(w, `{"error":"could not encode the response"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	w.Header().Set("Cache-Control", "no-store")

	w.WriteHeader(status)

	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, protocol.ErrorResponse{Error: message})
}

func (s *Server) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, protocol.ErrUnknownMachine.Error())

	case errors.Is(err, store.ErrPoolExhausted):
		writeError(w, http.StatusInsufficientStorage,
			"the coordination server has no free VPN addresses left in "+store.VPNRange)

	default:
		s.cfg.Logger.Printf("database error on %s %s: %v", r.Method, r.URL.Path, err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func requestContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 5*time.Second)
}
