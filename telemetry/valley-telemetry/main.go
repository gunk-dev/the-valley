// valley-telemetry serve is the collector: it answers one telemetry
// request on its standard input, which is a connection to the collector's
// socket that systemd accepted for it (Accept=yes), and exits.
//
// The only peer it serves is the git user, whose shell (valleyhook shell)
// has derived the principal from the key sshd authenticated and passed
// one request line for a command it parsed. The collector checks that
// line again, and the principal's grant, and then reads only what the
// manifest the host module generated declares: the units of each source,
// the streams producers export, and the reports it keeps itself. Nothing
// in a request names a file, a unit or a pattern.
//
// Anything running as the git user can connect to the socket and name any
// principal; the collector cannot tell such a process from the shell. The
// shared git account is its own trust boundary, and this is not a second
// one inside it.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"the-valley/telemetry"
)

const usage = `usage: valley-telemetry serve --manifest FILE --grants FILE --state DIR --peer-user NAME

Answers one telemetry request on standard input, a connection systemd
accepted on the collector's socket, and exits.`

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	os.Exit(serve(os.Args[2:]))
}

type collector struct {
	manifest  manifest
	grants    map[string]map[string]bool
	state     string
	out       *output
	now       func() time.Time
	systemctl func(units []string) ([]unitState, error)
	journal   func(units []string, n int, logs bool) ([]journalEntry, error)
	horizon   func(units []string) string
}

func serve(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "", "the source manifest")
	grantsPath := fs.String("grants", "", "the telemetry grants")
	state := fs.String("state", "", "the collector's own state directory")
	peer := fs.String("peer-user", "", "the one user that may connect")
	if err := fs.Parse(args); err != nil || *manifestPath == "" || *grantsPath == "" || *state == "" || *peer == "" {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	conn, err := net.FileConn(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "valley-telemetry: standard input is not a connection: %v\n", err)
		return 1
	}
	defer conn.Close()
	if err := checkPeer(conn, *peer); err != nil {
		fmt.Fprintf(os.Stderr, "valley-telemetry: %v\n", err)
		return 1
	}
	c := &collector{state: *state, now: time.Now, systemctl: systemctlShow, journal: journalRead, horizon: journalHorizon}
	c.out = newOutput(conn)
	if c.manifest, err = readManifest(*manifestPath); err == nil {
		c.grants, err = readGrants(*grantsPath)
	}
	if err != nil {
		c.out.fail("the collector's configuration does not read: %v", err)
		return 1
	}
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	in := bufio.NewReaderSize(conn, 8<<10)
	return c.handle(in, conn)
}

// handle answers one request: the line the shell sent, and for a report
// the records after it.
func (c *collector) handle(in *bufio.Reader, conn net.Conn) int {
	line, err := readLine(in, telemetry.MaxCommandBytes*2)
	if err != nil {
		c.out.fail("no request: %v", err)
		return 1
	}
	req, err := telemetry.DecodeRequest(line)
	if err == nil {
		err = req.Check()
	}
	if err != nil {
		c.out.fail("%v", err)
		return 1
	}
	if !c.grants[req.Principal][req.Grant()] {
		c.out.fail("%s does not hold the %s grant", req.Principal, req.Grant())
		return 1
	}
	release, err := c.slot(req)
	if err != nil {
		c.out.fail("%v", err)
		return 1
	}
	defer release()

	c.out.send(map[string]any{
		"kind": "header", "format": telemetry.Format, "op": req.Op, "host": c.manifest.Host,
		"server_time": telemetry.Stamp(c.now()), "collector": telemetry.Self("valley-telemetry", ""),
		"limits": map[string]any{"response_bytes": telemetry.MaxResponseBytes, "read_limit": req.Limit, "follow_seconds": req.Seconds},
	})
	switch req.Op {
	case telemetry.OpSources:
		c.sources(false)
		c.out.end(nil)
	case telemetry.OpSnapshot:
		c.sources(true)
		c.out.end(nil)
	case telemetry.OpRead:
		cur, err := c.read(req)
		if err != nil {
			c.out.fail("%v", err)
			return 1
		}
		c.out.end(cur)
	case telemetry.OpFollow:
		if conn != nil {
			conn.SetReadDeadline(time.Time{})
		}
		c.out.end(c.follow(req))
	case telemetry.OpReport:
		if conn != nil {
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		}
		if err := c.report(req.Principal, in); err != nil {
			c.out.fail("%v", err)
			return 1
		}
	}
	if c.out.err != nil {
		return 1
	}
	return 0
}

// slots: how many requests of one kind one principal may have open at
// once. The socket's own MaxConnections bounds them all together.
const (
	observeSlots = 4
	reportSlots  = 1
)

// slot takes one of the principal's slots for the request's kind, or
// says that all are taken. A slot is a lock file; the lock is the open
// file's, so a collector that dies releases it.
func (c *collector) slot(req telemetry.Request) (func(), error) {
	n, kind := observeSlots, "observe"
	if req.Op == telemetry.OpReport {
		n, kind = reportSlots, "report"
	}
	dir := filepath.Join(c.state, "slots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	for i := 0; i < n; i++ {
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%s.%s.%d", req.Principal, kind, i)), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			return func() { f.Close() }, nil
		}
		f.Close()
	}
	return nil, fmt.Errorf("%s already has %d %s requests open; try again when one ends", req.Principal, n, kind)
}

func readLine(in *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, more, err := in.ReadLine()
		if err != nil {
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > max {
			return nil, errors.New("line too long")
		}
		if !more {
			return line, nil
		}
	}
}

// checkPeer holds the connection to the one user allowed to make it.
func checkPeer(conn net.Conn, name string) error {
	u, err := user.Lookup(name)
	if err != nil {
		return fmt.Errorf("the peer user %s: %v", name, err)
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return errors.New("the connection is not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if credErr != nil {
		return credErr
	}
	if strconv.Itoa(int(cred.Uid)) != u.Uid {
		return fmt.Errorf("uid %d may not ask the collector anything", cred.Uid)
	}
	return nil
}

// manifest is what the host module declares: every source the host has,
// enabled or not.
type manifest struct {
	V         int      `json:"v"`
	Host      string   `json:"host"`
	Reporters []string `json:"reporters"`
	Sources   []source `json:"sources"`
}

type source struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Enabled     bool              `json:"enabled"`
	Location    string            `json:"location"` // local or remote
	Units       []string          `json:"units,omitempty"`
	Stream      string            `json:"stream,omitempty"`   // a stream a producer exports
	StateFile   string            `json:"state,omitempty"`    // the state a producer exports
	Reporter    string            `json:"reporter,omitempty"` // a remote source's reporting principal
	StaleAfter  int               `json:"stale_after_s,omitempty"`
	Configured  map[string]string `json:"configured,omitempty"`
	Instruments []string          `json:"instruments,omitempty"`
	Missing     []string          `json:"not_instrumented,omitempty"`
}

func readManifest(path string) (manifest, error) {
	var m manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	if m.V != 1 {
		return m, fmt.Errorf("manifest version %d is not 1", m.V)
	}
	for _, s := range m.Sources {
		if !telemetry.ValidSource(s.Name) || strings.HasPrefix(s.Name, "report.") {
			return m, fmt.Errorf("%q is not a source name", s.Name)
		}
	}
	return m, nil
}

// readGrants reads the telemetry grants: lines of a grant and a
// principal, as the push grants are written.
func readGrants(path string) (map[string]map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	g := map[string]map[string]bool{}
	for n, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("%s:%d is not a grant and a principal", path, n+1)
		}
		if g[f[1]] == nil {
			g[f[1]] = map[string]bool{}
		}
		g[f[1]][f[0]] = true
	}
	return g, nil
}

// output writes response lines, and stops at MaxResponseBytes or at the
// first write that fails or takes more than writeDeadline.
type output struct {
	w         io.Writer
	conn      net.Conn
	bytes     int
	truncated bool
	err       error
}

const writeDeadline = 10 * time.Second

func newOutput(conn net.Conn) *output { return &output{w: conn, conn: conn} }

// send writes one line, and reports whether the response has room for
// more.
func (o *output) send(v any) bool {
	if o.err != nil || o.truncated {
		return false
	}
	line, err := json.Marshal(v)
	if err != nil {
		o.err = err
		return false
	}
	if o.bytes+len(line) > telemetry.MaxResponseBytes-1024 {
		o.truncated = true
		return false
	}
	if o.conn != nil {
		o.conn.SetWriteDeadline(time.Now().Add(writeDeadline))
	}
	n, err := o.w.Write(append(line, '\n'))
	o.bytes += n
	if err != nil {
		o.err = err
		return false
	}
	return true
}

func (o *output) fail(format string, args ...any) {
	o.truncated = false
	o.send(map[string]any{"kind": "error", "error": fmt.Sprintf(format, args...)})
}

// end is the last line of a response that ended as it should: where a
// reader resumes from, and whether the response was cut short.
func (o *output) end(cur telemetry.Cursor) {
	t := o.truncated
	o.truncated = false
	line := map[string]any{"kind": "end", "truncated": t, "bytes": o.bytes}
	if cur != nil {
		line["cursor"] = cur.String()
	}
	o.send(line)
}
