// Command distributary is the LiveRecall server. The evaluator starts it
// through solution_server.py, which passes --port; every other flag must
// default to the configuration we want graded.
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
	"syscall"
	"time"

	"github.com/BarneyLaw/distributary/internal/naive"
)

func main() {
	port := flag.Int("port", 8765, "TCP port to listen on")
	host := flag.String("host", "0.0.0.0", "address to bind")
	mode := flag.String("mode", "naive", "server implementation: naive")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)

	var handler http.Handler
	// add cases for more modes later
	switch *mode {
	case "naive":
		handler = naive.New(logger)
	default:
		fmt.Fprintf(os.Stderr, "distributary: unknown --mode %q\n", *mode)
		os.Exit(2)
	}

	addr := net.JoinHostPort(*host, fmt.Sprint(*port))
	ln, err := net.Listen("tcp", addr)
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
