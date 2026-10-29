// Command valley-web-receive is the web export receiver. It takes the
// publisher's signed authorizations and uploads into a store, by the
// protocol package export defines, and valley-web serves what it activates
// as the newest authorization allows.
//
//	valley-web-receive --state FILE --listen ADDR --instance NAME
//	                   --destination NAME --verifiers FILE --token-file FILE
//	                   [--max-body SIZE] [--max-upload SIZE]
//	                   [--max-storage SIZE]
//
// It opens the store with export.OpenStore, which creates its layout and
// finishes an activation a crash interrupted, and it is the store's one
// writer. valley-web runs beside it as a process of its own, as another
// user, and opens the same store with export.OpenReadOnly. The two share
// nothing but the store: each has its own listener, its own limits and its
// own memory, so a saturated upload leaves valley-web serving.
//
// Every request must carry the token in --token-file. A request without
// it is refused as unauthenticated before anything else of it is read,
// and takes none of the receiver's request slots. The receiver serves
// until it is sent SIGINT or SIGTERM. It logs to standard error, and
// prints the address it listens on once it does.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"the-valley/note"
	"the-valley/web/export"
)

const usage = `usage: valley-web-receive --state FILE --listen ADDR --instance NAME
                          --destination NAME --verifiers FILE --token-file FILE
                          [--max-body SIZE] [--max-upload SIZE]
                          [--max-storage SIZE]

Receive signed authorizations and web export uploads from
valley-web-publish into a store that valley-web serves.

  --state FILE         the store's descriptor file, such as
                       /var/lib/valley-web/store; when FILE does not
                       exist, the receiver creates it and the payload
                       directory v2 beside it; the receiver is the store's
                       only writer, and refuses a directory
  --listen ADDR        TCP address to listen on, such as 127.0.0.1:8081;
                       port 0 picks a free port
  --instance NAME      instance name required in every manifest
  --destination NAME   destination name required in every manifest (this
                       receiver's name)
  --verifiers FILE     file of publisher verifier lines, one per line, as
                       written to <state>/verifier; every manifest must
                       verify under one of them
  --token-file FILE    file holding the bearer token required in every
                       request's Authorization header: one line of 32 to
                       256 visible ASCII characters, matching the file
                       passed to the publisher's --receiver-token
  --max-body SIZE      maximum declared byte size of a single body
                       (default 1G)
  --max-upload SIZE    maximum total declared byte size of the unique
                       bodies in one manifest, each SHA-256 counted once
                       (default 2G)
  --max-storage SIZE   best-effort cap on the disk space used by the
                       store's payload directory, including held
                       generations, staged bodies, and built repositories
                       (default 8G)

A SIZE is a positive byte count, optionally suffixed with K, M, G, or T
for KiB, MiB, GiB, or TiB.

valley-web-receive prints "valley-web-receive: listening on ADDR" once it
listens, and stops on SIGINT or SIGTERM.
`

// How many requests and connections the receiver takes at once. An
// authenticated upload request past maxRequests, or authorization request
// past maxAuthorizations, is answered 503 with no refusal, which the
// publisher retries after a pause. The two are counted apart, so uploads
// that fill their slots leave an authorization room. A connection past
// maxConns waits in the listen queue until another closes.
const (
	maxRequests       = 4
	maxAuthorizations = 2
	maxConns          = 32
)

// How long a request may take, from when its headers have been read. A
// commit fetches and checks every bundle it was sent. A stop gives the
// requests in flight stopTimeout to finish, and then ends them and waits
// drainTimeout more for them, and the git they run, to stop.
const (
	headerTimeout        = 10 * time.Second
	shortTimeout         = 10 * time.Second
	authorizationTimeout = 30 * time.Second
	manifestTimeout      = time.Minute
	// pruneTimeout bounds how long a prune after an authorization waits
	// for the readers of the active generation.
	pruneTimeout  = 5 * time.Minute
	bodyTimeout   = 30 * time.Minute
	commitTimeout = 30 * time.Minute
	idleTimeout   = time.Minute
	stopTimeout   = 10 * time.Second
	drainTimeout  = 5 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err := run(ctx, os.Args[1:], os.Stderr)
	var bad usageError
	switch {
	case errors.As(err, &bad):
		fmt.Fprintf(os.Stderr, "valley-web-receive: %v\n\n%s", err, usage)
		os.Exit(2)
	case err != nil:
		fmt.Fprintf(os.Stderr, "valley-web-receive: %v\n", err)
		os.Exit(1)
	}
}

// usageError is a command line valley-web-receive cannot run with.
type usageError struct{ error }

// run receives until ctx is done, then shuts the server down, letting
// requests in flight finish for a few seconds. It then ends the requests
// still running, and waits a few seconds more for them and the git they
// run to stop. A commit stopped part way leaves the active export as it
// was, as a crash does.
func run(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("valley-web-receive", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state", "", "")
	listen := flags.String("listen", "", "")
	instance := flags.String("instance", "", "")
	destination := flags.String("destination", "", "")
	verifiers := flags.String("verifiers", "", "")
	tokenFile := flags.String("token-file", "", "")
	maxBody, maxUpload, maxStorage := byteSize(1<<30), byteSize(2<<30), byteSize(8<<30)
	flags.Var(&maxBody, "max-body", "")
	flags.Var(&maxUpload, "max-upload", "")
	flags.Var(&maxStorage, "max-storage", "")
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
	case !export.ValidName(*instance):
		return usageError{fmt.Errorf("--instance %q must be 1 to 64 ASCII letters, digits, dots, dashes, or underscores, starting with a letter or digit", *instance)}
	case !export.ValidName(*destination):
		return usageError{fmt.Errorf("--destination %q must be 1 to 64 ASCII letters, digits, dots, dashes, or underscores, starting with a letter or digit", *destination)}
	case *verifiers == "":
		return usageError{errors.New("--verifiers is required")}
	case *tokenFile == "":
		return usageError{errors.New("--token-file is required")}
	}

	token, err := export.ReadToken(*tokenFile)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(*verifiers)
	if err != nil {
		return err
	}
	keys, err := note.ParseVerifierKeys(data)
	if err != nil {
		// The parser's error quotes the line, which would print a token
		// given here by mistake.
		return fmt.Errorf("cannot parse verifier lines in %s", *verifiers)
	}
	if len(keys) == 0 {
		return fmt.Errorf("%s contains no verifier lines", *verifiers)
	}

	logger := log.New(stderr, "valley-web-receive: ", log.LstdFlags)
	store, err := export.OpenStore(*state, func(err error) { logger.Print(err) })
	if err != nil {
		return err
	}
	store.MaxStorage = int64(maxStorage)
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	rv := &receiver{
		store: store, verifiers: keys, instance: *instance, destination: *destination, token: token,
		maxBody: int64(maxBody), maxUpload: int64(maxUpload),
		log: logger, slots: make(chan struct{}, maxRequests), authSlots: make(chan struct{}, maxAuthorizations),
		pruneDelay: 100 * time.Millisecond,
	}
	srv := &http.Server{
		Handler: rv,
		// The server's own handler for OPTIONS * reads the body before
		// any check, with no deadline. Every request goes to rv instead,
		// which refuses one without the token before it reads anything.
		DisableGeneralOptionsHandler: true,
		ReadHeaderTimeout:            headerTimeout,
		IdleTimeout:                  idleTimeout,
		MaxHeaderBytes:               16 << 10,
		ErrorLog:                     logger,
	}
	fmt.Fprintf(stderr, "valley-web-receive: listening on %s\n", ln.Addr())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(limitListener(ln, maxConns)) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		// Requests still running are ended: closing their connections
		// cancels their contexts, which kills the process group of the
		// git a commit runs.
		// Close returns without waiting for them, and a process that
		// exited first could leave that git running.
		srv.Close()
		drained, cancel := context.WithTimeout(context.Background(), drainTimeout)
		defer cancel()
		if !rv.drain(drained) {
			logger.Printf("requests were still running %v after connections were closed", drainTimeout)
		}
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// limitListener accepts at most n connections at once. Accept waits for
// one to close before it takes another, or for the listener to be closed.
func limitListener(ln net.Listener, n int) net.Listener {
	return &limited{Listener: ln, slots: make(chan struct{}, n), closed: make(chan struct{})}
}

type limited struct {
	net.Listener
	slots  chan struct{}
	closed chan struct{}
	once   sync.Once
}

// Accept returns net.ErrClosed once the listener is closed, even while it
// waits for a slot. Server.Shutdown and Server.Close wait for Accept to
// return, so a wait that closing the listener did not end would hold up
// the stop for as long as every connection stayed open.
func (l *limited) Accept() (net.Conn, error) {
	select {
	case l.slots <- struct{}{}:
	case <-l.closed:
		return nil, net.ErrClosed
	}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.slots
		return nil, err
	}
	return &limitedConn{Conn: c, release: sync.OnceFunc(func() { <-l.slots })}, nil
}

func (l *limited) Close() error {
	l.once.Do(func() { close(l.closed) })
	return l.Listener.Close()
}

type limitedConn struct {
	net.Conn
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.release()
	return err
}

// byteSize is a size flag's value in bytes. It is set from a number, or a
// number with K, M, G or T for 1024 to the first to fourth power, as
// systemd reads a size and the publisher reads its own.
type byteSize int64

func (b *byteSize) Set(v string) error {
	number, unit := v, int64(1)
	if n := len(v); n > 1 {
		if i := strings.IndexByte("KMGT", v[n-1]); i >= 0 {
			number, unit = v[:n-1], 1<<(10*(i+1))
		}
	}
	x, err := strconv.ParseInt(number, 10, 64)
	if err != nil || x <= 0 || x > math.MaxInt64/unit {
		return fmt.Errorf("invalid size %q: must be a positive byte count, optionally suffixed with K, M, G, or T", v)
	}
	*b = byteSize(x * unit)
	return nil
}

func (b *byteSize) String() string { return strconv.FormatInt(int64(*b), 10) }
