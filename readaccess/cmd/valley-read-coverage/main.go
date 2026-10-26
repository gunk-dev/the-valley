// valley-read-coverage lists the projects a principal cannot read at a
// destination, and which of the two consents each lacks: the project's own
// policy/read, or a read grant in the instance's identity registry. It reads
// the instance repository and each project's main on a valley host, and
// changes nothing. It repairs no project and bypasses no policy.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"the-valley/readaccess"
)

const usage = `usage: valley-read-coverage --instance-repo DIR --destination NAME
                            --reader PRINCIPAL [--registry-dir DIR]
                            [--read-schema FILE] [--identity-schema FILE]

List the projects PRINCIPAL cannot read at the destination NAME, and the
consent each lacks: the project's own, its policy/read on its main, or the
instance's, a read grant in the identity registry on the instance
repository's main.

  --instance-repo DIR   an absolute local path to the instance repository,
                        whose refs/the-valley/projects lists the projects;
                        each project's repository is beside it, named
                        <project>.git when DIR ends in .git, and <project>
                        otherwise
  --destination NAME    the destination of the edge read boundary
  --reader PRINCIPAL    the principal, or anyone for a reader with no
                        credential
  --registry-dir DIR    the registry's directory in the instance
                        repository's main (default identity)
  --read-schema FILE    schema/read.cue (default $VALLEY_READ_SCHEMA)
  --identity-schema FILE
                        schema/identity.cue (default
                        $VALLEY_IDENTITY_SCHEMA)

It prints one line for each project PRINCIPAL cannot read, then how many
projects PRINCIPAL may read. It exits 0 when it read every project, whether
or not any lacks a consent, 1 when it could not read one, and 2 for invalid
flags.`

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, time.Now()))
}

type config struct {
	instanceRepo, destination, reader, registryDir string
	access                                         readaccess.Reader
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, now time.Time) int {
	var c config
	fs := flag.NewFlagSet("valley-read-coverage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.instanceRepo, "instance-repo", "", "")
	fs.StringVar(&c.destination, "destination", "", "")
	fs.StringVar(&c.reader, "reader", "", "")
	fs.StringVar(&c.registryDir, "registry-dir", "identity", "")
	fs.StringVar(&c.access.ReadSchema, "read-schema", os.Getenv("VALLEY_READ_SCHEMA"), "")
	fs.StringVar(&c.access.IdentitySchema, "identity-schema", os.Getenv("VALLEY_IDENTITY_SCHEMA"), "")
	err := fs.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprintln(stdout, usage)
		return 0
	case err != nil:
	case fs.NArg() > 0:
		err = fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case !filepath.IsAbs(c.instanceRepo):
		err = errors.New("--instance-repo must be an absolute local path")
	case c.destination == "" || c.reader == "":
		err = errors.New("--destination and --reader are required")
	case c.access.ReadSchema == "" || c.access.IdentitySchema == "":
		err = errors.New("no schema: give --read-schema and --identity-schema, or set VALLEY_READ_SCHEMA and VALLEY_IDENTITY_SCHEMA")
	}
	if err != nil {
		fmt.Fprintf(stderr, "valley-read-coverage: %v\n%s\n", err, usage)
		return 2
	}
	if err := c.report(ctx, stdout, stderr, now); err != nil {
		fmt.Fprintf(stderr, "valley-read-coverage: %v\n", err)
		return 1
	}
	return 0
}

// report reads the registry and every project's policy/read, and says
// which consents each project lacks.
func (c *config) report(ctx context.Context, stdout, stderr io.Writer, now time.Time) error {
	r := &c.access
	if err := r.CheckSchemas(ctx); err != nil {
		return err
	}
	tip, ok, err := revParse(ctx, c.instanceRepo, "refs/heads/main^{commit}")
	if err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%s has no main", c.instanceRepo)
	}
	grants, err := r.Grants(ctx, c.instanceRepo, tip, c.registryDir, c.destination, now)
	var bad *readaccess.InvalidError
	switch {
	case errors.Is(err, readaccess.ErrMissing):
		fmt.Fprintf(stderr, "valley-read-coverage: the instance repository's main holds no registry in %s/, so it grants no read\n", c.registryDir)
	case errors.As(err, &bad):
		fmt.Fprintf(stderr, "valley-read-coverage: the registry in %s/ is not valid, so it grants no read: %v\n", c.registryDir, err)
	case err != nil:
		return fmt.Errorf("could not read the registry: %w", err)
	}

	list, ok, err := revParse(ctx, c.instanceRepo, "refs/the-valley/projects")
	if err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%s publishes no refs/the-valley/projects", c.instanceRepo)
	}
	names, err := git(ctx, c.instanceRepo, "cat-file", "blob", list)
	if err != nil {
		return err
	}
	parent, base := filepath.Split(strings.TrimRight(c.instanceRepo, "/"))
	suffix := ""
	if strings.HasSuffix(base, ".git") {
		suffix = ".git"
	}

	total, readable := 0, 0
	for _, name := range strings.Split(string(names), "\n") {
		if name == "" {
			continue
		}
		if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
			return fmt.Errorf("refs/the-valley/projects lists %q, which is not a project name", name)
		}
		total++
		consent, why, err := c.consent(ctx, filepath.Join(parent, name+suffix), name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		var lacks []string
		if consent == nil {
			lacks = append(lacks, "no project consent ("+why+")")
		} else if !consent.Names(c.reader) {
			lacks = append(lacks, fmt.Sprintf("no project consent (its policy/read does not name %s)", c.reader))
		}
		switch {
		case consent != nil && !grants.Covers(c.reader, name, consent.Identity):
			lacks = append(lacks, fmt.Sprintf("no instance grant (no read grant of %s at %s covers %s with the identity %s)",
				c.reader, c.destination, name, consent.Identity))
		case consent == nil && !grants.Selects(c.reader, name):
			lacks = append(lacks, fmt.Sprintf("no instance grant (no read grant of %s at %s names %s)", c.reader, c.destination, name))
		}
		if len(lacks) == 0 {
			readable++
			continue
		}
		fmt.Fprintf(stdout, "%s: %s\n", name, strings.Join(lacks, "; "))
	}
	fmt.Fprintf(stdout, "%s may read %d of %d projects at %s\n", c.reader, readable, total, c.destination)
	return nil
}

// consent reads a project's consent at its main. A project with no main,
// no policy/read or an invalid one has none, and why says so. Any other
// failure is an error.
func (c *config) consent(ctx context.Context, repo, name string) (*readaccess.Consent, string, error) {
	tip, ok, err := revParse(ctx, repo, "refs/heads/main^{commit}")
	if err != nil {
		return nil, "", err
	} else if !ok {
		return nil, "it has no main", nil
	}
	consent, err := c.access.Consent(ctx, repo, tip, name)
	var bad *readaccess.InvalidError
	switch {
	case errors.Is(err, readaccess.ErrMissing):
		return nil, "its main holds no " + readaccess.PolicyDir, nil
	case errors.As(err, &bad):
		return nil, "its " + readaccess.PolicyDir + " is not valid: " + err.Error(), nil
	case err != nil:
		return nil, "", err
	}
	return &consent, "", nil
}

// revParse resolves rev in repo. ok is false when the repository holds no
// such ref; an error is a repository git could not read.
func revParse(ctx context.Context, repo, rev string) (string, bool, error) {
	out, err := git(ctx, repo, "rev-parse", "--verify", "--quiet", "--end-of-options", rev)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(out)), true, nil
}

// git runs git in repo with replacement refs off, and without the
// variables that would point it at another repository.
func git(ctx context.Context, repo string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects", "-C", repo}, args...)...)
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE":
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, err
		}
		return nil, fmt.Errorf("git %s in %s: %w: %s", args[0], repo, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
