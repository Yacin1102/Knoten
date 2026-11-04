package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"knoten/internal/daemon"
)

func main() {

	configPath := flag.String("config", daemon.DefaultConfigPath, "path to the meshd config file")

	setup := flag.Bool("setup", false, "ask the configuration questions interactively and write the config file")

	force := flag.Bool("force", false, "with -setup: overwrite an existing config (WARNING: this gives the machine a NEW key)")

	showKey := flag.Bool("show-key", false, "print this machine's WireGuard public key and exit")

	flag.Parse()

	logger := log.New(os.Stderr, "meshd: ", log.LstdFlags)

	if *showKey {
		if err := showPublicKey(*configPath); err != nil {
			fatalf(logger, "%v", err)
		}
		return
	}

	if *setup {
		if err := daemon.Setup(*configPath, *force); err != nil {
			fatalf(logger, "%v", err)
		}
		fmt.Println("config written; start meshd to bring the tunnel up")
		return
	}

	// LOAD THE CONFIGURATION
	cfg, err := daemon.LoadConfig(*configPath)
	if err != nil {
		fatalf(logger, "%v", err)
	}

	d, err := daemon.New(cfg, *configPath, logger)
	if err != nil {
		fatalf(logger, "%v", err)
	}

	// SIGNALS
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	syncNow := make(chan struct{}, 1)
	go func() {
		for range hup {
			logger.Printf("SIGHUP received; checking the config and syncing now")
			select {
			case syncNow <- struct{}{}:
			default:
			}
		}
	}()

	// RUN
	if err := d.Run(ctx, syncNow); err != nil {
		fatalf(logger, "%v", err)
	}

	logger.Printf("stopped")
}

func showPublicKey(configPath string) error {
	cfg, err := daemon.LoadConfig(configPath)
	if err != nil {
		return err
	}

	if cfg.PrivateKey.IsZero() {
		return fmt.Errorf("no key yet in %s; run meshd once to generate one", configPath)
	}

	fmt.Println(cfg.PrivateKey.PublicKey())
	return nil
}

func fatalf(logger *log.Logger, format string, args ...any) {
	logger.Printf("FATAL: "+format, args...)
	os.Exit(1)
}
