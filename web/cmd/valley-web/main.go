// Command valley-web serves a receiver's store to its readers, as the
// store's authorization allows: the active export's plan, its knots, and a
// source browser over the main of each project the reader may read.
//
//	valley-web --state FILE --listen ADDR [--origin URL]
//
// It opens the store with export.OpenReadOnly, which writes nothing to
// it, so it may run as a user that can only read the receiver's store. It
// serves it with package server until it is sent SIGINT or SIGTERM. It
// logs to standard error, and prints the address it listens on once it
// does.
//
// With --origin, valley-web also signs readers in with the read links the
// host issues. It keeps the links redeemed and their sessions in browser/
// beside the store's descriptor, which the deployment makes for its user
// alone (package readerstate). That directory is the one place it writes.
// It first makes its process non-dumpable (package nodump), so no link a
// reader posts and no session token reaches a core dump or another
// process of its user. Without --origin, it reads every request as anyone.
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
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"the-valley/web/export"
	"the-valley/web/nodump"
	"the-valley/web/readerstate"
	"the-valley/web/server"
)

const usage = `usage: valley-web --state FILE --listen ADDR [--origin URL]

Serve the active export of a web export receiver's store to its readers,
as the store's authorization allows: the plan page, each knot, and a
source browser over main for each project the reader may read.

  --state FILE   the receiver's existing store descriptor, such as
                 /var/lib/valley-web/store; valley-web opens the store
                 read-only and writes nothing there
  --listen ADDR  the TCP address to listen on, such as 127.0.0.1:8080;
                 port 0 picks a free port
  --origin URL   the public HTTPS origin readers reach, such as
                 https://example.org: a scheme and a host, with a port
                 only when it is not 443. With it, readers sign in with
                 the host's read links, and valley-web keeps their
                 sessions in browser/ beside the store's descriptor,
                 which must exist, owned by valley-web's user, mode 0700,
                 and the process makes itself non-dumpable, which needs
                 Linux. Without it, every request is read as anyone.

valley-web prints "valley-web: listening on ADDR" once it listens, and
stops on SIGINT or SIGTERM.
`

// readerOptions are the options the browser state is opened with. Tests
// set its fault hook.
var readerOptions readerstate.Options

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
	origin := flags.String("origin", "", "")
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
	case *origin != "" && !validOrigin(*origin):
		return usageError{fmt.Errorf("--origin %q is not an HTTPS origin: https:// and a lowercase host, with a port only when it is not 443, and nothing after", *origin)}
	}

	logger := log.New(stderr, "valley-web: ", log.LstdFlags)
	store, err := export.OpenReadOnly(*state)
	if err != nil {
		return err
	}
	c := server.Config{Store: store, Origin: *origin, Log: func(err error) { logger.Print(err) }}
	if *origin != "" {
		if err := nodump.Set(); err != nil {
			return fmt.Errorf("could not keep readers' links and sessions out of a core dump: %w", err)
		}
		abs, err := filepath.Abs(*state)
		if err != nil {
			return err
		}
		if c.Readers, err = readerstate.Open(filepath.Join(filepath.Dir(abs), "browser"), readerOptions); err != nil {
			return fmt.Errorf("opening the browser state: %w", err)
		}
		defer c.Readers.Close()
		if err := c.Readers.Err(); err != nil {
			logger.Printf("the browser state is unavailable until valley-web restarts, so no reader signs in and anyone is served: %v", err)
		}
	}
	handler := server.New(c)
	handler.Prepare(ctx)
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler,
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

// validOrigin reports whether s is an HTTPS origin as a browser sends it
// in an Origin header: https://, a lowercase host, a port only when it is
// not 443, and no path, query or anything else.
func validOrigin(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Opaque == "" && u.User == nil && u.Host != "" &&
		u.Host == strings.ToLower(u.Host) && u.Port() != "443" && !strings.HasSuffix(u.Host, ":") &&
		s == "https://"+u.Host
}
