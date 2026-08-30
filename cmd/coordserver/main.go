package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"knoten/internal/server"
	"knoten/internal/store"
)

func main() {
	listenAddr := flag.String("listen", ":8080","address to listen on")

	dbPath := flag.String("db", "/var/lib/knoten/coord.db","path to the SQLite database file; it is created if missing")

	cidrFlag := flag.String("cidr", "10.42.0.0/16","the private address range to hand out to machines, e.g. 10.42.0.0/16")

	tokenFlag := flag.String("token", "","shared join token (INSECURE: visible in `ps`; prefer -token-file)")

	tokenFile := flag.String("token-file", "","path to a file containing the shared join token (recommended over -token)")

	syncInterval := flag.Duration("sync-interval", 30*time.Second,"how often daemons are told to poll; the server dictates this to the whole fleet")

	peerTimeout := flag.Duration("peer-timeout", 90*time.Second,"how long a machine may stay silent before it is left out of peer lists (should be ~3x sync-interval)")

	trustProxy := flag.Bool("trust-proxy", false,"trust the X-Forwarded-For header for the caller's IP; ONLY enable this behind a reverse proxy you control")

	flag.Parse()

	logger := log.New(os.Stderr, "coordserver: ", log.LstdFlags)
	
	cidr, err := store.ParseCIDR(*cidrFlag)
	if err != nil {
		fatalf(logger, "invalid -cidr: %v", err)
	}

	if *peerTimeout <= *syncInterval {
		fatalf(logger,
			"-peer-timeout (%s) must be longer than -sync-interval (%s); otherwise a single missed poll drops a healthy machine out of every peer list",
			*peerTimeout, *syncInterval)
	}

	joinToken, err := resolveToken(*tokenFlag, *tokenFile)
	if err != nil {
		fatalf(logger, "could not read the join token: %v", err)
	}

	dbDir := filepath.Dir(*dbPath)
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		fatalf(logger, "could not create the database directory %s: %v", dbDir, err)
	}

	st, err := store.Open(*dbPath, cidr)
	if err != nil {
		fatalf(logger, "could not open the database: %v", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Printf("error while closing the database: %v", err)
		}
	}()

	srv, err := server.New(server.Config{
		Store:        st,
		JoinToken:    joinToken,
		SyncInterval: *syncInterval,
		PeerTimeout:  *peerTimeout,
		TrustProxy:   *trustProxy,
		Logger:       logger,
	})
	if err != nil {
		fatalf(logger, "could not build the server: %v", err)
	}

	httpServer := &http.Server{
		Addr:    *listenAddr,
		Handler: srv.Handler(),

		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       120 * time.Second,
		
		ErrorLog: logger,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)

	go func() {
		logger.Printf("listening on %s (plain HTTP)", *listenAddr)
		serverErr <- httpServer.ListenAndServe()
	}()

	logger.Printf("VPN range: %s", cidr.String())
	logger.Printf("database:  %s", *dbPath)
	logger.Printf("machines poll every %s; considered gone after %s of silence", *syncInterval, *peerTimeout)
	if joinToken == "" {
		logger.Printf("WARNING: no join token set; ANY machine that can reach this port may join and read the peer list")
	} else {
		logger.Printf("join token is set (%d characters)", len(joinToken))
	}
	if *trustProxy {
		logger.Printf("WARNING: -trust-proxy is on; X-Forwarded-For is believed; make sure a proxy you control OVERWRITES that header")
	}

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatalf(logger, "server stopped: %v", err)
		}

	case <-ctx.Done():
		logger.Printf("shutdown signal received; finishing in-flight requests")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Printf("graceful shutdown did not finish in time: %v", err)
			_ = httpServer.Close()
		}
	}

	logger.Printf("stopped")
}

func resolveToken(inline, file string) (string, error) {
	if inline != "" && file != "" {
		return "", fmt.Errorf("use either -token or -token-file, not both")
	}

	if file == "" {
		return inline, nil
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("could not read %s: %w", file, err)
	}

	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("%s is empty", file)
	}
	return token, nil
}

func fatalf(logger *log.Logger, format string, args ...any) {
	logger.Printf("FATAL: "+format, args...)
	os.Exit(1)
}