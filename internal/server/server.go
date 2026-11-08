// Package server is the coordination server. It keeps, in memory only, the
// machines that synced recently and tells each one about the others. Machines
// work out their own addresses and decide which peers to use; the server only
// passes on what each machine says about itself.
package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"knoten/internal/protocol"
)

type Config struct {
	JoinToken    string
	SyncInterval time.Duration
	PeerTimeout  time.Duration
	TrustProxy   bool
	Logger       *log.Logger
}

// machine is what the server knows about one machine, as the machine reported it.
type machine struct {
	name     string
	subnet   string
	network  string
	endpoint string // the address the server saw it at, with its listen port
	lastSeen time.Time
}

type Server struct {
	cfg      Config
	mu       sync.Mutex
	machines map[string]machine // by public key
}

func New(cfg Config) (*Server, error) {
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
	return &Server{cfg: cfg, machines: map[string]machine{}}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+protocol.PathSync, s.handleSync)
	return limitBody(mux)
}

// handleSync records the caller and answers with every other machine that
// synced within the peer timeout. Machines silent for longer are forgotten.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	var req protocol.SyncRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.checkToken(req.JoinToken) {
		writeError(w, http.StatusUnauthorized, "invalid join token")
		return
	}

	now := time.Now()
	caller := machine{
		name:     req.Name,
		subnet:   req.Subnet,
		network:  req.Network,
		endpoint: net.JoinHostPort(observedIP(r, s.cfg.TrustProxy), strconv.Itoa(req.ListenPort)),
		lastSeen: now,
	}

	s.mu.Lock()
	_, known := s.machines[req.PublicKey]
	s.machines[req.PublicKey] = caller

	peers := make([]protocol.Peer, 0, len(s.machines))
	for pub, m := range s.machines {
		switch {
		case now.Sub(m.lastSeen) > s.cfg.PeerTimeout:
			delete(s.machines, pub)
		case pub != req.PublicKey:
			peers = append(peers, protocol.Peer{
				PublicKey: pub,
				Name:      m.name,
				Subnet:    m.subnet,
				Network:   m.network,
				Endpoint:  m.endpoint,
			})
		}
	}
	s.mu.Unlock()

	if !known {
		s.cfg.Logger.Printf("%q (%s) joined from %s", caller.name, shortKey(req.PublicKey), caller.endpoint)
	}

	writeJSON(w, http.StatusOK, protocol.SyncResponse{
		Peers:               peers,
		SyncIntervalSeconds: int(s.cfg.SyncInterval.Seconds()),
	})
}

const maxBodyBytes = 64 * 1024

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
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
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
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
		writeError(w, http.StatusInternalServerError, "could not encode the response")
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

func shortKey(k string) string {
	const n = 8
	if len(k) <= n {
		return k
	}
	return k[:n] + "…"
}