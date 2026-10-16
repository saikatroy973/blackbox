// Command blackbox is the Blackbox server: a flight recorder for AI agents.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/saikat/blackbox/internal/server"
	"github.com/saikat/blackbox/internal/store"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "blackbox:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return serve(nil)
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "ping":
		return ping(args[1:])
	case "version":
		fmt.Println("blackbox", version)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q (run: blackbox help)", args[0])
	}
}

func usage() {
	fmt.Print(`blackbox - flight recorder for AI agents

Usage:
  blackbox serve [-addr :8080]   start the server
  blackbox ping [-url URL]       check that a server is healthy
  blackbox version               print version
  blackbox help                  show this help
`)
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "listen address")
	dbPath := fs.String("db", "blackbox.db", `SQLite database path (use ":memory:" for an ephemeral database)`)
	if err := fs.Parse(args); err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.OpenSQLite(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("closing store", "error", err)
		}
	}()

	handler, err := server.New(server.Config{Version: version, Store: st})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("blackbox listening", "addr", *addr, "version", version, "db", *dbPath)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func ping(args []string) error {
	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	url := fs.String("url", "http://127.0.0.1:8080/healthz", "health endpoint URL")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(*url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s", resp.Status)
	}
	fmt.Println("ok")
	return nil
}
