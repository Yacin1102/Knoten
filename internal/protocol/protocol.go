// Package protocol defines the JSON messages that meshd and the coordination
// server exchange. A machine POSTs a SyncRequest to PathSync and gets a
// SyncResponse back. Errors come back as an ErrorResponse.
package protocol

const (
	PathSync     = "/v2/sync"
)

const DefaultSyncIntervalSeconds = 30

type SyncRequest struct {
	PublicKey  string `json:"public_key"`
	Name       string `json:"name"`
	ListenPort int    `json:"listen_port"`
	Subnet     string `json:"subnet"`
	Network    string `json:"network"` // the first 48 bits of the sender's address

	JoinToken string `json:"join_token,omitempty"`
}


type Peer struct {
	PublicKey string `json:"public_key"`
	Name      string `json:"name"`
	Subnet    string `json:"subnet"`
	Network   string `json:"network"`
	Endpoint  string `json:"endpoint"`
}

type SyncResponse struct {
	Peers               []Peer `json:"peers"`
	SyncIntervalSeconds int    `json:"sync_interval_seconds"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}
