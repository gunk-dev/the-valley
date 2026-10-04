package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// grants maps a principal to the grants it holds.
type grants map[string]map[string]bool

func (g grants) holds(principal, grant string) bool {
	return g[principal][grant]
}

// read adds what one grants file grants. Its lines are a grant and a
// principal, separated by space; blank lines and lines starting with # are
// skipped. Two writers produce these files: the identity compiler, from the
// registry (identity/), and the NixOS module, from what the host declares
// by hand. Holdings add up across files, the way the compiled
// authorized_keys adds to the declared keys.
//
// A file that does not exist grants nothing. The compiled file is missing
// until the first compilation, and the hook then refuses what only a grant
// opens, which is the direction a missing grant has to default in.
//
// A grant this program does not check is kept and never consulted, so a
// compiled file that names a grant from a newer registry costs nothing. A
// line that is not a grant and a principal fails the read, and the hook
// then refuses the push: a file it cannot parse is a file it cannot vouch
// for.
func (g grants) read(path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	lines := bufio.NewScanner(f)
	for n := 1; lines.Scan(); n++ {
		line := strings.TrimSpace(lines.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return fmt.Errorf("%s:%d: %q is not a grant and a principal", path, n, line)
		}
		grant, principal := fields[0], fields[1]
		if g[principal] == nil {
			g[principal] = map[string]bool{}
		}
		g[principal][grant] = true
	}
	return lines.Err()
}
