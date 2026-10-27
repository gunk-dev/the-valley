// valley-web-publish publishes a valley host's public projects to a web
// receiver: the projects anyone may read there. One run is one pass. It reads the valley through the CLI's JSON
// commands, projects what it read onto the public export (package export),
// signs a manifest for it, and uploads it by the protocol package export
// defines. The receiver never connects to the host; its replies are all the
// publisher hears from it.
//
// A pass, in order:
//
//  1. take the lock in the state directory, so two passes never overlap;
//  2. read the export key from the state directory, or make one on the
//     first start, say its verifier line whenever it changes, and hold the
//     state and the caches to --max-storage before anything is fetched;
//  3. fetch main, refs/the-valley/projects and --registry-ref of the
//     instance repository into the publisher's own bare repository, whose
//     origin it is, once for the whole pass, refuse a main that carries
//     policy/valley, and read the read grants at --destination from the
//     identity registry at the --registry-ref it fetched;
//  4. prepare the valley CLI's clone of each project a read grant to
//     anyone names, and of no other, so the CLI reads no other project;
//  5. run valley plan --all --json and valley pending --all --json in the
//     pointer repository, whose main names the instance repository at
//     policy/valley, so they discover the projects and the signers through
//     the instance repository's own publications and never read its main;
//     and hold both listings to the project list it fetched. A project no
//     grant names has no clone, so both list it as one they could not
//     read, and the pass keeps it private;
//  6. for each project a read grant to anyone names, fetch main into a
//     mirror under the cache and read the project's own policy/read there;
//  7. run valley knot show --project P <id> --json for every knot of every
//     public project, in the publisher's repository, and build the export
//     with export.Build, reading again when the inputs were read at
//     different graph tips; then hold the state and the caches to
//     --max-storage again;
//  8. bundle each public project's main, writing each bundle only into
//     what --max-bundles and --max-storage leave;
//  9. upload it, unless the receiver acknowledged the same content digest
//     less than --recheck ago, or write it to --out instead.
//
// A project is public when anyone may read it: its own policy/read names
// anyone (schema/read.cue), and a read grant to anyone at the destination
// covers it (schema/identity.cue). Package readaccess reads and evaluates
// both. A policy or registry that is missing, or that its schema refuses,
// consents to nothing, so the project is private. A project a grant names
// that cannot be read fails the pass, which then sends nothing: an export
// without it would take it off the web. A project no grant names is
// private whether or not it can be read.
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"the-valley/readaccess"
	"the-valley/web/export"
)

const usage = `usage: valley-web-publish --instance-repo URL --instance NAME --destination NAME
                          (--receiver URL | --out DIR) [--principals FILE]
                          [--state DIR] [--read-schema FILE]
                          [--identity-schema FILE] [--registry-ref REF]
                          [--registry-dir DIR] [--recheck DURATION]
                          [--max-bundles SIZE] [--max-storage SIZE]

One pass of the web publisher: read the valley's public projects, and
upload them to the receiver at URL, or write them to DIR.

  --instance-repo URL  an absolute local path to the instance repository,
                       the origin of the publisher's own bare repository,
                       which the valley
                       CLI discovers projects and signers through; its
                       main holds policy/instance and no policy/valley;
                       URL holds no #, no control character, and no space
                       at either end
  --instance NAME      the instance the manifest names, and the export
                       key's signing name, NAME/web-export
  --destination NAME   the receiver the manifest is for, and the destination
                       of the edge read boundary whose read grants decide
                       what anyone may read there
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
  --read-schema FILE   schema/read.cue (default $VALLEY_READ_SCHEMA)
  --identity-schema FILE
                       schema/identity.cue (default $VALLEY_IDENTITY_SCHEMA)
  --registry-ref REF   the ref of the instance repository whose tip carries
                       the identity registry, as the identity compiler
                       reads it: a full ref name (default refs/heads/main);
                       a pass reads the registry at the one commit it
                       fetched, and an instance repository with no REF
                       holds no registry, so every project is private
  --registry-dir DIR   the identity registry's directory in that commit
                       (default identity)
  --recheck DURATION   how long an acknowledgement is trusted: a pass whose
                       digest the receiver acknowledged less than this ago
                       sends nothing (default 1h)
  --max-bundles SIZE   the most the bundles of one export may hold
                       together; a bundle is written only into what it
                       leaves, and a pass past it sends nothing
                       (default 1G)
  --max-storage SIZE   the most the state directory and the caches may
                       hold, checked before each repository cache write
                       and after collection; a bundle is written only into
                       what it leaves, and a pass past it sends nothing
                       (default 16G)

A SIZE is a number of bytes, or a number with K, M, G or T for KiB, MiB,
GiB or TiB.

The cache is $XDG_CACHE_HOME (default $HOME/.cache): the valley CLI's
clones under valley/, the publisher's repositories, mirrors and bundles
under valley-web-publish/.`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, err := parseArgs(args)
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
	readSchema   string
	idSchema     string
	registryRef  string
	registryDir  string
	recheck      time.Duration
	maxBundles   byteSize
	maxStorage   byteSize
}

func parseArgs(args []string) (config, error) {
	c := config{maxBundles: 1 << 30, maxStorage: 16 << 30}
	fs := flag.NewFlagSet("valley-web-publish", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.instanceRepo, "instance-repo", "", "")
	fs.StringVar(&c.instance, "instance", "", "")
	fs.StringVar(&c.destination, "destination", "", "")
	fs.StringVar(&c.receiver, "receiver", "", "")
	fs.StringVar(&c.out, "out", "", "")
	fs.StringVar(&c.principals, "principals", "", "")
	fs.StringVar(&c.state, "state", "", "")
	fs.StringVar(&c.readSchema, "read-schema", os.Getenv("VALLEY_READ_SCHEMA"), "")
	fs.StringVar(&c.idSchema, "identity-schema", os.Getenv("VALLEY_IDENTITY_SCHEMA"), "")
	fs.StringVar(&c.registryRef, "registry-ref", "refs/heads/main", "")
	fs.StringVar(&c.registryDir, "registry-dir", "identity", "")
	fs.DurationVar(&c.recheck, "recheck", time.Hour, "")
	fs.Var(&c.maxBundles, "max-bundles", "")
	fs.Var(&c.maxStorage, "max-storage", "")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	switch {
	case fs.NArg() > 0:
		return c, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case c.instanceRepo == "":
		return c, errors.New("--instance-repo is required")
	case strings.ContainsFunc(c.instanceRepo, func(r rune) bool { return r == '#' || r < 0x20 || r == 0x7f }),
		strings.TrimSpace(c.instanceRepo) != c.instanceRepo:
		// The pointer repository names it in policy/valley, which the CLI
		// reads up to a # and without the spaces around it.
		return c, fmt.Errorf("--instance-repo %q holds a #, a control character or a space at either end, which policy/valley cannot name", c.instanceRepo)
	case !filepath.IsAbs(c.instanceRepo):
		return c, errors.New("--instance-repo must be an absolute local path")
	case !export.ValidName(c.instance):
		return c, fmt.Errorf("--instance %q is not a name: it is 1 to 64 letters, digits, dots, dashes and underscores, opening with a letter or a digit", c.instance)
	case !export.ValidName(c.destination):
		return c, fmt.Errorf("--destination %q is not a name: it is 1 to 64 letters, digits, dots, dashes and underscores, opening with a letter or a digit", c.destination)
	case (c.receiver == "") == (c.out == ""):
		return c, errors.New("give exactly one of --receiver and --out")
	case !readaccess.ValidRef(c.registryRef):
		return c, fmt.Errorf("--registry-ref %q is not a full ref name: it opens with refs/, and git check-ref-format accepts it", c.registryRef)
	case c.readSchema == "" || c.idSchema == "":
		return c, errors.New("no schema: give --read-schema and --identity-schema, or set VALLEY_READ_SCHEMA and VALLEY_IDENTITY_SCHEMA")
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

// byteSize is a size flag's value in bytes. It is set from a number, or a
// number with K, M, G or T for 1024 to the first to fourth power, as
// systemd reads a size.
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
		return fmt.Errorf("%q is not a size: a positive number of bytes, or one with K, M, G or T", v)
	}
	*b = byteSize(x * unit)
	return nil
}

func (b *byteSize) String() string { return strconv.FormatInt(int64(*b), 10) }

// publisher is one pass's configuration and what it reads and writes
// through: its state and cache directories, its key, the receiver, and
// the clock.
type publisher struct {
	cfg            config
	stdout, stderr io.Writer

	// cache is the publisher's own cache, under $XDG_CACHE_HOME. origin is
	// its bare repository, whose origin is the instance repository. pointer
	// is the repository whose main names the instance repository at
	// policy/valley (preparePointer); the CLI calls it _pointer, which is
	// no project's name.
	collectionRoom int64
	cache          string
	origin         string
	pointer        string

	principals export.Principals
	signer     string
	key        ed25519.PrivateKey

	// access reads the two consents to read access.
	access readaccess.Reader

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
		cache: cache, origin: filepath.Join(cache, "origin.git"), pointer: filepath.Join(cache, "_pointer.git"),
		principals: principals,
		signer:     cfg.instance + "/web-export",
		access:     readaccess.Reader{ReadSchema: cfg.readSchema, IdentitySchema: cfg.idSchema, CueTimeout: cueTimeout},
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
	if err := p.checkStorage(); err != nil {
		return err
	}
	read, err := p.collect(ctx)
	if err != nil {
		return err
	}
	if err := p.checkStorage(); err != nil {
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
	p.say("public: %s; private: %s", listNames(public), listNames(r.private))
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
