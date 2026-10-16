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
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/saikat/blackbox/internal/eval"
	"github.com/saikat/blackbox/internal/replay"
	"github.com/saikat/blackbox/internal/seed"
	"github.com/saikat/blackbox/internal/server"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/pkg/llm"
)

var version = "dev"

// openStore opens a SQLite database, or Postgres when the DSN is a
// postgres:// URL.
func openStore(ctx context.Context, dsn string) (store.Store, error) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		return store.OpenPostgres(ctx, dsn)
	}
	return store.OpenSQLite(ctx, dsn)
}

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
	case "replay":
		return replayCmd(args[1:])
	case "eval":
		return evalCmd(args[1:])
	case "seed":
		return seedCmd(args[1:])
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
  blackbox serve [-addr :8080]    start the server
  blackbox ping [-url URL]        check that a server is healthy
  blackbox replay [-db PATH] ID   re-run a recorded trace deterministically
  blackbox eval run -suite FILE   run an eval suite (-dir DIR for a directory)
  blackbox seed [-db PATH]        record demo traces, replay and evals
  blackbox version                print version
  blackbox help                   show this help
`)
}

// errReplayDiverged is returned when a replay does not match the
// recorded run. The report has already been printed.
var errReplayDiverged = errors.New("replay diverged from the original run")

func replayCmd(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	dbPath := fs.String("db", "blackbox.db", "SQLite path or postgres:// DSN")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: blackbox replay [-db path] <trace-id>")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openStore(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	report, err := replay.New(st).Run(ctx, fs.Arg(0))
	if err != nil {
		return err
	}

	fmt.Printf("Replaying trace %s (%d spans, %d cassettes)\n", report.TraceID, report.Spans, report.Cassettes)
	fmt.Printf("Replay trace:  %s\n", report.ReplayTraceID)
	fmt.Printf("Cassette hits: %d, divergences: %d\n", report.Hits, report.Misses)

	if report.Misses > 0 {
		fmt.Println("Divergences:")
		for _, d := range report.Divergences {
			fmt.Printf("  - %s %q: %s\n", d.Kind, d.Name, d.Detail)
		}
		err := errReplayDiverged
		if report.RunErr != nil {
			err = fmt.Errorf("%w: %v", errReplayDiverged, report.RunErr)
		}
		return err
	}
	if report.RunErr != nil {
		return report.RunErr
	}
	fmt.Println("Replay matches the original run.")
	return nil
}

// errEvalFailed is returned when an eval suite has failing cases or
// regressions. Results have already been printed.
var errEvalFailed = errors.New("eval failed")

func seedCmd(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	dbPath := fs.String("db", "blackbox.db", "SQLite path or postgres:// DSN")
	evalsDir := fs.String("evals", "evals", "directory of suite YAML files to run (empty to skip)")
	runs := fs.Int("runs", 0, "number of demo traces to record (0 = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openStore(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	report, err := seed.Run(ctx, st, seed.Options{Runs: *runs, EvalsDir: *evalsDir})
	if err != nil {
		return err
	}
	fmt.Printf("Seeded %d traces (plus 1 failing trace and 1 replay)\n", len(report.Traces))
	if report.EvalSuites > 0 {
		fmt.Printf("Recorded %d eval suite run(s) covering %d case(s)\n", report.EvalSuites, report.EvalCases)
	}
	fmt.Println("Run `blackbox serve` and open http://localhost:8080/ui/ to explore.")
	return nil
}

func evalCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: blackbox eval run -suite file.yaml | -dir dir [-db path]")
	}
	switch args[0] {
	case "run":
		return evalRun(args[1:])
	default:
		return fmt.Errorf("unknown eval subcommand %q (run: blackbox help)", args[0])
	}
}

func evalRun(args []string) error {
	fs := flag.NewFlagSet("eval run", flag.ContinueOnError)
	suitePath := fs.String("suite", "", "path to a suite YAML file")
	dir := fs.String("dir", "", "directory of suite YAML files")
	dbPath := fs.String("db", "blackbox.db", "SQLite path or postgres:// DSN")
	savePath := fs.String("save", "", "write the run result as JSON")
	baselinePath := fs.String("baseline", "", "compare against a saved run JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *suitePath == "" && *dir == "" {
		return errors.New("usage: blackbox eval run -suite file.yaml | -dir evals [-db path] [-save out.json] [-baseline base.json]")
	}

	var paths []string
	if *suitePath != "" {
		paths = append(paths, *suitePath)
	}
	if *dir != "" {
		for _, pattern := range []string{"*.yaml", "*.yml"} {
			matches, err := filepath.Glob(filepath.Join(*dir, pattern))
			if err != nil {
				return err
			}
			paths = append(paths, matches...)
		}
	}
	if len(paths) == 0 {
		return fmt.Errorf("no suite files found")
	}
	sort.Strings(paths)
	if *savePath != "" && len(paths) > 1 {
		return errors.New("-save requires a single suite")
	}

	var baseline *eval.RunResult
	if *baselinePath != "" {
		b, err := eval.LoadRunResult(*baselinePath)
		if err != nil {
			return err
		}
		baseline = &b
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openStore(ctx, *dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	runner := eval.New(st, llm.NewMock(), "mock-1")
	failed := false
	for _, path := range paths {
		suite, err := eval.LoadSuite(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		result, err := runner.Run(ctx, suite)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		printEvalResult(result)
		if result.Failed > 0 {
			failed = true
		}
		if baseline != nil {
			regressions := eval.Compare(*baseline, result)
			if len(regressions) > 0 {
				fmt.Printf("Regressions vs baseline (%d):\n", len(regressions))
				for _, rg := range regressions {
					fmt.Printf("  - %s: %s\n", rg.Case, rg.Reason)
				}
				failed = true
			} else if baseline.Suite == result.Suite {
				fmt.Println("No regressions vs baseline.")
			}
		}
		if *savePath != "" {
			if err := eval.SaveRunResult(*savePath, result); err != nil {
				return err
			}
		}
	}
	if failed {
		return errEvalFailed
	}
	return nil
}

func printEvalResult(res eval.RunResult) {
	fmt.Printf("Suite %s: %d/%d cases passed (cost $%.6f, %dms)\n",
		res.Suite, res.Passed, res.Total, res.TotalCostUSD, res.DurationMS)
	for _, c := range res.Cases {
		status := "PASS"
		if !c.Passed {
			status = "FAIL"
		}
		fmt.Printf("  %s  %-24s %d steps  $%.6f  %s\n", status, c.Name, c.Steps, c.CostUSD, c.TraceID)
		for _, f := range c.Failures {
			fmt.Printf("        - %s\n", f)
		}
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "listen address")
	dbPath := fs.String("db", "blackbox.db", `SQLite path or postgres:// DSN (":memory:" for ephemeral SQLite)`)
	if err := fs.Parse(args); err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := openStore(ctx, *dbPath)
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
