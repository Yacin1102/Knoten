package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"time"
	_ "modernc.org/sqlite"
)

type Machine struct {
	PublicKey  string
	Name       string
	VPNIP      string
	Endpoint   string
	ListenPort int
	CreatedAt  int64
	LastSeen   int64
}

var ErrNotFound = errors.New("machine not found")

type Store struct {
	db   *sql.DB
	cidr *net.IPNet
}

const schema = `
CREATE TABLE IF NOT EXISTS machines (
    public_key  TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    vpn_ip      TEXT NOT NULL UNIQUE,
    endpoint    TEXT NOT NULL DEFAULT '',
    listen_port INTEGER NOT NULL DEFAULT 51820,
    created_at  INTEGER NOT NULL,
    last_seen   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_machines_last_seen ON machines(last_seen);
`

func Open(path string, cidr *net.IPNet) (*Store, error) {
	if cidr == nil {
		return nil, errors.New("store.Open needs an address range")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("could not open the database at %s: %w", path, err)
	}

	db.SetMaxOpenConns(1)

	db.SetConnMaxLifetime(0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("could not reach the database at %s: %w", path, err)
	}

	pragmas := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA synchronous = FULL`,
	}

	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("could not apply %q: %w", p, err)
		}
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("could not create the database schema: %w", err)
	}

	return &Store{db: db, cidr: cidr}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CIDR() string {
	return s.cidr.String()
}

func (s *Store) RegisterMachine(ctx context.Context, in Machine) (Machine, error) {
	now := time.Now().Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Machine{}, fmt.Errorf("could not start a database transaction: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var existing Machine
	err = tx.QueryRowContext(ctx,
		`SELECT public_key, name, vpn_ip, endpoint, listen_port, created_at, last_seen
		   FROM machines WHERE public_key = ?`,
		in.PublicKey,
	).Scan(
		&existing.PublicKey, &existing.Name, &existing.VPNIP,
		&existing.Endpoint, &existing.ListenPort, &existing.CreatedAt, &existing.LastSeen,
	)

	switch {
		
	case errors.Is(err, sql.ErrNoRows):

	case err != nil:
		return Machine{}, fmt.Errorf("could not look up the machine: %w", err)

	default:
		if _, err := tx.ExecContext(ctx,
			`UPDATE machines
			    SET name = ?, endpoint = ?, listen_port = ?, last_seen = ?
			  WHERE public_key = ?`,
			in.Name, in.Endpoint, in.ListenPort, now, in.PublicKey,
		); err != nil {
			return Machine{}, fmt.Errorf("could not update the machine: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return Machine{}, fmt.Errorf("could not save the changes: %w", err)
		}

		existing.Name = in.Name
		existing.Endpoint = in.Endpoint
		existing.ListenPort = in.ListenPort
		existing.LastSeen = now
		return existing, nil
	}


	rows, err := tx.QueryContext(ctx, `SELECT vpn_ip FROM machines`)
	if err != nil {
		return Machine{}, fmt.Errorf("could not list the used addresses: %w", err)
	}

	used := make(map[string]bool)
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			_ = rows.Close()
			return Machine{}, fmt.Errorf("could not read an address row: %w", err)
		}
		used[ip] = true
	}

	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Machine{}, fmt.Errorf("could not read all the used addresses: %w", err)
	}

	if err := rows.Close(); err != nil {
		return Machine{}, fmt.Errorf("could not finish reading the addresses: %w", err)
	}

	vpnIP, err := NextFreeIP(s.cidr, used)
	if err != nil {
		return Machine{}, err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO machines
		    (public_key, name, vpn_ip, endpoint, listen_port, created_at, last_seen)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.PublicKey, in.Name, vpnIP, in.Endpoint, in.ListenPort, now, now,
	); err != nil {

		return Machine{}, fmt.Errorf("could not store the new machine: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Machine{}, fmt.Errorf("could not save the new machine: %w", err)
	}

	return Machine{
		PublicKey:  in.PublicKey,
		Name:       in.Name,
		VPNIP:      vpnIP,
		Endpoint:   in.Endpoint,
		ListenPort: in.ListenPort,
		CreatedAt:  now,
		LastSeen:   now,
	}, nil
}

func (s *Store) Sync(ctx context.Context, publicKey, endpoint string, listenPort int, aliveSince time.Time) (Machine, []Machine, error) {
	now := time.Now().Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Machine{}, nil, fmt.Errorf("could not start a database transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`UPDATE machines
		    SET endpoint = ?, listen_port = ?, last_seen = ?
		  WHERE public_key = ?`,
		endpoint, listenPort, now, publicKey,
	)
	if err != nil {
		return Machine{}, nil, fmt.Errorf("could not update the caller: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return Machine{}, nil, fmt.Errorf("could not confirm the update: %w", err)
	}
	if affected == 0 {
		return Machine{}, nil, ErrNotFound
	}

	var self Machine
	if err := tx.QueryRowContext(ctx,
		`SELECT public_key, name, vpn_ip, endpoint, listen_port, created_at, last_seen
		   FROM machines WHERE public_key = ?`,
		publicKey,
	).Scan(
		&self.PublicKey, &self.Name, &self.VPNIP,
		&self.Endpoint, &self.ListenPort, &self.CreatedAt, &self.LastSeen,
	); err != nil {
		return Machine{}, nil, fmt.Errorf("could not read the caller's row: %w", err)
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT public_key, name, vpn_ip, endpoint, listen_port, created_at, last_seen
		   FROM machines
		  WHERE public_key != ? AND last_seen >= ?
		  ORDER BY vpn_ip`,
		publicKey, aliveSince.Unix(),
	)
	if err != nil {
		return Machine{}, nil, fmt.Errorf("could not list the peers: %w", err)
	}

	var peers []Machine

	for rows.Next() {
		var m Machine
		if err := rows.Scan(
			&m.PublicKey, &m.Name, &m.VPNIP,
			&m.Endpoint, &m.ListenPort, &m.CreatedAt, &m.LastSeen,
		); err != nil {
			_ = rows.Close()
			return Machine{}, nil, fmt.Errorf("could not read a peer row: %w", err)
		}

		peers = append(peers, m)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return Machine{}, nil, fmt.Errorf("could not read all the peers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Machine{}, nil, fmt.Errorf("could not finish reading the peers: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Machine{}, nil, fmt.Errorf("could not save the sync: %w", err)
	}

	return self, peers, nil
}

func (s *Store) CountMachines(ctx context.Context, aliveSince time.Time) (total int, alive int, err error) {
	
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM machines`).Scan(&total); err != nil {
		return 0, 0, fmt.Errorf("could not count the machines: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM machines WHERE last_seen >= ?`, aliveSince.Unix(),
	).Scan(&alive); err != nil {
		return 0, 0, fmt.Errorf("could not count the live machines: %w", err)
	}

	return total, alive, nil
}