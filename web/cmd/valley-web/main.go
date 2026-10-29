// Command valley-web serves a receiver's store to anonymous readers, as
// the store's authorization allows: the active export's plan, its knots,
// and a source browser over the main of each project anyone may read.
//
//	valley-web --state FILE --listen ADDR
//
// It opens the store with export.OpenReadOnly, which writes nothing to
// it, so it may run as a user that can only read the receiver's store. It
// serves it with package server until it is sent SIGINT or SIGTERM. It
// logs to standard error, and prints the address it listens on once it
// does.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"the-valley/web/export"
	"the-valley/web/server"
)

const usage = `usage: valley-web --state FILE --listen ADDR

Serve the active export of a web export receiver's store to anonymous
readers, as the store's authorization allows: the plan page, each knot,
and a source browser over the main of each project anyone may read.

  --state FILE   the receiver's store descriptor, which must exist, such
                 as /var/lib/valley-web/store; valley-web only reads the
                 store, and writes nothing there
  --listen ADDR  the TCP address to listen on, such as 127.0.0.1:8080;
                 port 0 picks a free port

valley-web prints "valley-web: listening on ADDR" once it listens, and
stops on SIGINT or SIGTERM.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err := run(ctx, os.Args[1:], os.Stderr)
	var bad usageError
	switch {
	case errors.As(err, &bad):
		fmt.Fprintf(os.Stderr, "valley-web: %v\n\n%s", err, usage)
		os.Exit(2)
	case err != nil:
		fmt.Fprintf(os.Stderr, "valley-web: %v\n", err)
		os.Exit(1)
	}
}

// usageError is a command line valley-web cannot run with.
type usageError struct{ error }

// run serves until ctx is done, then shuts the server down, letting
// requests in flight finish for a few seconds.
func run(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("valley-web", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state", "", "")
	listen := flags.String("listen", "", "")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(stderr, usage)
		return nil
	} else if err != nil {
		return usageError{err}
	}
	switch {
	case flags.NArg() > 0:
		return usageError{fmt.Errorf("unexpected argument %q", flags.Arg(0))}
	case *state == "":
		return usageError{errors.New("--state is required")}
	case *listen == "":
		return usageError{errors.New("--listen is required")}
	}

	logger := log.New(stderr, "valley-web: ", log.LstdFlags)
	store, err := export.OpenReadOnly(*state)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           server.New(server.Config{Store: store, Log: func(err error) { logger.Print(err) }}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          logger,
	}
	fmt.Fprintf(stderr, "valley-web: listening on %s\n", ln.Addr())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return err
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
