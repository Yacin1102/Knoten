package server

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"knoten/internal/protocol"
	"knoten/internal/store"
)

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req protocol.RegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if !s.checkToken(req.JoinToken) {
		writeError(w, http.StatusUnauthorized, "invalid join token")
		return
	}

	if err := protocol.ValidatePublicKey(req.PublicKey); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := protocol.ValidateName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := protocol.ValidateListenPort(req.ListenPort); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	endpoint := joinHostPort(observedIP(r, s.cfg.TrustProxy), req.ListenPort)

	ctx, cancel := requestContext(r)
	defer cancel()

	machine, err := s.cfg.Store.RegisterMachine(ctx, store.Machine{
		PublicKey:  req.PublicKey,
		Name:       req.Name,
		Endpoint:   endpoint,
		ListenPort: req.ListenPort,
	})
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	s.cfg.Logger.Printf("registered %q (%s) as %s, endpoint %s",
		machine.Name, shortKey(machine.PublicKey), machine.VPNIP, machine.Endpoint)

	writeJSON(w, http.StatusOK, protocol.RegisterResponse{
		VPNIP: machine.VPNIP,
		VPNCIDR: s.cfg.Store.CIDR(),
		Endpoint: machine.Endpoint,
	})
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	var req protocol.SyncRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if !s.checkToken(req.JoinToken) {
		writeError(w, http.StatusUnauthorized, "invalid join token")
		return
	}

	if err := protocol.ValidatePublicKey(req.PublicKey); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := protocol.ValidateListenPort(req.ListenPort); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	endpoint := joinHostPort(observedIP(r, s.cfg.TrustProxy), req.ListenPort)

	aliveSince := time.Now().Add(-s.cfg.PeerTimeout)

	ctx, cancel := requestContext(r)
	defer cancel()

	self, peers, err := s.cfg.Store.Sync(ctx, req.PublicKey, endpoint, req.ListenPort, aliveSince)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	
	writeJSON(w, http.StatusOK, protocol.SyncResponse{

		Self: toProtocolPeer(self),
		Peers: toProtocolPeers(peers),
		SyncIntervalSeconds: int(s.cfg.SyncInterval.Seconds()),
	})
}

type healthResponse struct {
	Status          string `json:"status"`
	MachinesTotal   int    `json:"machines_total"`
	MachinesAlive   int    `json:"machines_alive"`
	UptimeSeconds   int64  `json:"uptime_seconds"`
	VPNCIDR         string `json:"vpn_cidr"`
	SyncIntervalSec int    `json:"sync_interval_seconds"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := requestContext(r)
	defer cancel()

	total, alive, err := s.cfg.Store.CountMachines(ctx, time.Now().Add(-s.cfg.PeerTimeout))
	if err != nil {
		s.cfg.Logger.Printf("health check failed: %v", err)
		writeError(w, http.StatusServiceUnavailable, "database is not reachable")
		return
	}

	writeJSON(w, http.StatusOK, healthResponse{
		Status:          "ok",
		MachinesTotal:   total,
		MachinesAlive:   alive,
		UptimeSeconds:   int64(time.Since(s.startedAt).Seconds()),
		VPNCIDR:         s.cfg.Store.CIDR(),
		SyncIntervalSec: int(s.cfg.SyncInterval.Seconds()),
	})
}

func toProtocolPeer(m store.Machine) protocol.Peer {
	return protocol.Peer{
		PublicKey: m.PublicKey,
		Name:      m.Name,
		VPNIP:     m.VPNIP,
		Endpoint:  m.Endpoint,
		LastSeen:  m.LastSeen,
	}
}

func toProtocolPeers(machines []store.Machine) []protocol.Peer {
	out := make([]protocol.Peer, 0, len(machines))
	for _, m := range machines {
		out = append(out, toProtocolPeer(m))
	}
	return out
}

func joinHostPort(ip string, port int) string {
	if containsColon(ip) {
		return "[" + ip + "]:" + strconv.Itoa(port)
	}
	return ip + ":" + strconv.Itoa(port)
}

func containsColon(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return true
		}
	}
	return false
}

func shortKey(k string) string {
	const n = 8
	if len(k) <= n {
		return k
	}
	return fmt.Sprintf("%s…", k[:n])
}