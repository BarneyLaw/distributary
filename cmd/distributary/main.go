// Command distributary is the LiveRecall server. The evaluator starts it
// through solution_server.py, which passes --port; every other flag must
// default to the configuration we want graded.
//
// Modes:
//
//	naive  Go port of the professor's starter server (the "before" baseline)
//	live   our server: router -> source handler -> session -> fanout
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/BarneyLaw/distributary/internal/fanout"
	"github.com/BarneyLaw/distributary/internal/naive"
	"github.com/BarneyLaw/distributary/internal/server"
	"github.com/BarneyLaw/distributary/internal/session"
	"github.com/BarneyLaw/distributary/internal/source"
)

func main() {
	port := flag.Int("port", 8765, "TCP port to listen on")
	host := flag.String("host", "0.0.0.0", "address to bind")
	// The harness can only pass --port, so DISTRIBUTARY_MODE picks the mode
	// for public-test runs (DISTRIBUTARY_MODE=naive make t3). The evaluator
	// never sets it, so the fallback below is what gets graded: switch it to
	// "live" once live passes T1 and T4.
	mode := flag.String("mode", envOr("DISTRIBUTARY_MODE", "naive"), "server implementation: naive or live")
	clientDir := flag.String("client-dir", "", "browser client files to serve on plain HTTP (default: client/ next to bin/)")
	mss := flag.Int("mss", 1400, "TCP max segment size for accepted connections; 0 leaves the OS default")
	statsEvery := flag.Duration("stats", 10*time.Second, "how often to log source counters; 0 disables")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)

	var handler http.Handler
	switch *mode {
	case "naive":
		handler = naive.New(logger)
	case "live":
		sess := session.New(session.DefaultConfig())
		src := source.New(sess, logger)
		handler = &server.Router{
			Sources: src,
			Clients: fanout.NewHub(sess),
			Log:     logger,
			Static:  staticFiles(*clientDir, logger),
		}
		if *statsEvery > 0 {
			go logStats(src, *statsEvery, logger)
		}
	default:
		fmt.Fprintf(os.Stderr, "distributary: unknown --mode %q\n", *mode)
		os.Exit(2)
	}

	addr := net.JoinHostPort(*host, fmt.Sprint(*port))
	ln, err := listen(addr, *mss)
	if err != nil {
		logger.Fatalf("listen %s: %v", addr, err)
	}
	logger.Printf("distributary (%s) listening on ws://%s", *mode, addr)

	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	// run_demo.py and submission_check.py stop the server with SIGTERM between
	// runs. Closing the listener lets main return, which ends the process and
	// every open connection with it, so the port is free for the next run.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		srv.Close()
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("serve: %v", err)
	}
}

// staticFiles serves the browser client on the same port as the WebSocket,
// so the page can connect back to its own origin. It looks in dir if given,
// otherwise in client/ next to the bin/ folder holding this executable
// (the launcher runs bin/distributary-linux-<arch>), then in ./client.
// Returns nil if none exists; plain HTTP requests then get 426.
func staticFiles(dir string, logger *log.Logger) http.Handler {
	candidates := []string{dir}
	if dir == "" {
		candidates = nil
		if exe, err := os.Executable(); err == nil {
			if exe, err = filepath.EvalSymlinks(exe); err == nil {
				candidates = append(candidates, filepath.Join(filepath.Dir(exe), "..", "client"))
			}
		}
		candidates = append(candidates, "client")
	}
	for _, c := range candidates {
		if st, err := os.Stat(filepath.Join(c, "index.html")); err == nil && !st.IsDir() {
			logger.Printf("serving browser client from %s", c)
			return http.FileServer(http.Dir(c))
		}
	}
	logger.Printf("browser client not found; plain HTTP requests will get 426")
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func logStats(src *source.Handler, every time.Duration, logger *log.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for range t.C {
		logger.Printf("source: %s", src.Stats())
	}
}