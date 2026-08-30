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
	"knoten/internal/wg"
)

func main() {
	
	configPath := flag.String("config", daemon.DefaultConfigPath,"path to the meshd config file")

	setup := flag.Bool("setup", false,"ask the configuration questions interactively and write the config file")

	force := flag.Bool("force", false,"with -setup: overwrite an existing config (WARNING: this machine keeps its key, but re-check your answers)")

	once := flag.Bool("once", false,"perform a single register+sync+write cycle and exit, instead of running forever")

	showKey := flag.Bool("show-key", false,"print this machine's WireGuard public key and exit")

	printDefault := flag.Bool("print-default-config", false,"print a configuration template to stdout and exit")

	flag.Parse()

	logger := log.New(os.Stderr, "meshd: ", log.LstdFlags)

	if *printDefault {
		if err := daemon.PrintDefaultConfig(); err != nil {
			fatalf(logger, "%v", err)
		}
		return
	}

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
		*once = true
	}

	// LOAD THE CONFIGURATION
	cfg, err := daemon.LoadConfig(*configPath)
	if err != nil {
		fatalf(logger, "%v", err)
	}

	d, err := daemon.New(cfg, logger)
	if err != nil {
		fatalf(logger, "%v", err)
	}

	// SIGNALS
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)

	refresh := make(chan struct{}, 1)
	go func() {
		for range hup {
			logger.Printf("SIGHUP received; syncing now")
			select {
			case refresh <- struct{}{}:
			default:
			}
		}
	}()

	// RUN
	if err := d.Run(ctx, *once, refresh); err != nil {
		fatalf(logger, "%v", err)
	}

	logger.Printf("stopped")
}

func showPublicKey(configPath string) error {
	cfg, err := daemon.LoadConfig(configPath)
	if err != nil {
		return err
	}

	st, err := daemon.LoadState(cfg.StatePath)
	if err != nil {
		return err
	}

	if st.PrivateKey == "" {
		return fmt.Errorf("no key yet at %s; run meshd once to generate one", cfg.StatePath)
	}

	pub, err := wg.PublicKeyFrom(st.PrivateKey)
	if err != nil {
		return err
	}

	fmt.Println(pub)
	return nil
}

func fatalf(logger *log.Logger, format string, args ...any) {
	logger.Printf("FATAL: "+format, args...)
	os.Exit(1)
}