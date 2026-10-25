// valley-web-publish publishes a valley host's public projects to a web
// receiver. One run is one pass. It reads the valley through the CLI's JSON
// commands, projects what it read onto the public export (package export),
// signs a manifest for it, and uploads it by the protocol package export
// defines. The receiver never connects to the host; its replies are all the
// publisher hears from it.
//
// A pass, in order:
//
//  1. take the lock in the state directory, so two passes never overlap;
//  2. read the export key from the state directory, or make one on the
//     first start, and say its verifier line whenever it changes;
//  3. fetch main of the instance repository into the publisher's own bare
//     repository, whose origin it is, and read the instance's policy/web;
//  4. run valley plan --all --json and valley pending --all --json in that
//     repository, which discover the projects and the signers through the
//     host's own publications;
//  5. for each project the instance allows, fetch main into a mirror under
//     the cache and read the project's own policy/web there;
//  6. run valley knot show --project P <id> --json for every knot of every
//     public project, and build the export with export.Build, reading
//     again when the inputs were read at different graph tips;
//  7. upload it, unless the receiver acknowledged the same content digest
//     less than --recheck ago, or write it to --out instead.
//
// A project is public only when the instance's policy/web and its own both
// say public (schema/web.cue). A declaration that is missing, or that the
// schema refuses, means private.
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"the-valley/webui/export"
)

const usage = `usage: valley-web-publish --instance-repo URL --instance NAME --destination NAME
                          (--receiver URL | --out DIR) [--principals FILE]
                          [--state DIR] [--schema FILE] [--recheck DURATION]

One pass of the web publisher: read the valley's public projects, and
upload them to the receiver at URL, or write them to DIR.

  --instance-repo URL  the instance repository: the origin of the
                       publisher's own bare repository, which the valley
                       CLI discovers projects through
  --instance NAME      the instance the manifest names, and the export
                       key's signing name, NAME/web-export
  --destination NAME   the receiver the manifest is for
  --receiver URL       the receiver's base URL, http or https
  --out DIR            write the export to DIR, which must not exist or be
                       empty, instead of uploading it: manifest, export.json
                       and bundles/<project>.bundle
  --principals FILE    the display mapping: a JSON object from each
                       principal's name to the name an export shows; a
                       name it does not map is shown as contributor
  --state DIR          the key, its verifier line, the lock and the last
                       acknowledgement (default
                       ${XDG_STATE_HOME:-$HOME/.local/state}/valley/web-publish)
  --schema FILE        schema/web.cue (default $VALLEY_WEB_SCHEMA)
  --recheck DURATION   how long an acknowledgement is trusted: a pass whose
                       digest the receiver acknowledged less than this ago
                       sends nothing (default 1h)

The cache is $XDG_CACHE_HOME (default $HOME/.cache): the valley CLI's
clones under valley/, the publisher's repository and mirrors under
valley-web-publish/.`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(stdout, usage)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "valley-web-publish: %v\n%s\n", err, usage)
		return 2
	}
	p, err := newPublisher(cfg, stdout, stderr)
	if err == nil {
		err = p.pass(ctx)
	}
	if err != nil {
		fmt.Fprintf(stderr, "valley-web-publish: %v\n", err)
		return 1
	}
	return 0
}

// config is a pass's flags.
type config struct {
	instanceRepo string
	instance     string
	destination  string
	receiver     string
	out          string
	principals   string
	state        string
	schema       string
	recheck      time.Duration
}

func parseArgs(args []string, stderr io.Writer) (config, error) {
	var c config
	fs := flag.NewFlagSet("valley-web-publish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.instanceRepo, "instance-repo", "", "")
	fs.StringVar(&c.instance, "instance", "", "")
	fs.StringVar(&c.destination, "destination", "", "")
	fs.StringVar(&c.receiver, "receiver", "", "")
	fs.StringVar(&c.out, "out", "", "")
	fs.StringVar(&c.principals, "principals", "", "")
	fs.StringVar(&c.state, "state", "", "")
	fs.StringVar(&c.schema, "schema", os.Getenv("VALLEY_WEB_SCHEMA"), "")
	fs.DurationVar(&c.recheck, "recheck", time.Hour, "")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	switch {
	case fs.NArg() > 0:
		return c, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case c.instanceRepo == "":
		return c, errors.New("--instance-repo is required")
	case !export.ValidName(c.instance):
		return c, fmt.Errorf("--instance %q is not a name: it is 1 to 64 letters, digits, dots, dashes and underscores, opening with a letter or a digit", c.instance)
	case !export.ValidName(c.destination):
		return c, fmt.Errorf("--destination %q is not a name: it is 1 to 64 letters, digits, dots, dashes and underscores, opening with a letter or a digit", c.destination)
	case (c.receiver == "") == (c.out == ""):
		return c, errors.New("give exactly one of --receiver and --out")
	case c.schema == "":
		return c, errors.New("no schema: give --schema, or set VALLEY_WEB_SCHEMA")
	case c.recheck < 0:
		return c, errors.New("--recheck is not negative")
	}
	if c.receiver != "" {
		if err := checkReceiver(c.receiver); err != nil {
			return c, err
		}
	}
	if c.state == "" {
		base := os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return c, errors.New("no state directory: give --state, or set XDG_STATE_HOME or HOME")
			}
			base = filepath.Join(home, ".local", "state")
		}
		c.state = filepath.Join(base, "valley", "web-publish")
	}
	return c, nil
}

// publisher is one pass's configuration and what it reads and writes
// through: its state and cache directories, its key, the receiver, and
// the clock.
type publisher struct {
	cfg            config
	stdout, stderr io.Writer

	// cache is the publisher's own cache, under $XDG_CACHE_HOME. origin is
	// its bare repository, whose origin is the instance repository.
	cache  string
	origin string

	principals export.Principals
	signer     string
	key        ed25519.PrivateKey

	client *http.Client
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
}

func newPublisher(cfg config, stdout, stderr io.Writer) (*publisher, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, errors.New("no cache directory: set XDG_CACHE_HOME or HOME")
		}
		base = filepath.Join(home, ".cache")
	}
	if !filepath.IsAbs(base) {
		return nil, fmt.Errorf("XDG_CACHE_HOME is %q, which is not an absolute path", base)
	}
	principals, err := readPrincipals(cfg.principals)
	if err != nil {
		return nil, err
	}
	cache := filepath.Join(base, "valley-web-publish")
	return &publisher{
		cfg: cfg, stdout: stdout, stderr: stderr,
		cache: cache, origin: filepath.Join(cache, "origin.git"),
		principals: principals,
		signer:     cfg.instance + "/web-export",
		client:     newClient(),
		now:        time.Now,
		sleep:      sleepContext,
	}, nil
}

// pass is one run of the publisher.
func (p *publisher) pass(ctx context.Context) error {
	if err := os.MkdirAll(p.cfg.state, 0o700); err != nil {
		return err
	}
	unlock, err := lockFile(filepath.Join(p.cfg.state, "lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if p.key, err = p.loadKey(); err != nil {
		return err
	}
	read, err := p.collect(ctx)
	if err != nil {
		return err
	}
	digest, err := read.export.Digest()
	if err != nil {
		return err
	}
	p.sayVisibility(read)
	if p.cfg.out != "" {
		return p.writeOut(ctx, read)
	}
	ack, err := p.readAcknowledged()
	if err != nil {
		return err
	}
	if ack.covers(p.cfg, digest, p.now(), p.cfg.recheck) {
		p.say("the receiver acknowledged digest %s at %s, and nothing has changed since; nothing is sent", short(digest), ack.At)
		return nil
	}
	return p.upload(ctx, read, digest, ack)
}

// sayVisibility says which projects this pass exports, and which it keeps
// private.
func (p *publisher) sayVisibility(r *reading) {
	var public []string
	for _, proj := range r.export.Projects {
		public = append(public, proj.Name)
	}
	list := func(names []string) string {
		if len(names) == 0 {
			return "none"
		}
		return strings.Join(names, ", ")
	}
	p.say("public: %s; private: %s", list(public), list(r.private))
}

func (p *publisher) say(format string, args ...any) {
	fmt.Fprintf(p.stdout, "valley-web-publish: "+format+"\n", args...)
}

func (p *publisher) warn(format string, args ...any) {
	fmt.Fprintf(p.stderr, "valley-web-publish: "+format+"\n", args...)
}

// short is the first twelve characters of a digest or an object ID, for
// messages.
func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
