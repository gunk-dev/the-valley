package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// lingeringProcesses names the commands still running with a working directory
// inside dir: background work a worker left when its turn ended. Workers'
// tools often start commands in a new session, so the worktree, not the
// process group, ties them to the run. This process, its ancestors (the
// pane's shell), and processes attached to another terminal (someone's
// interactive shell) are skipped. It reads /proc where there is one, as on
// Linux, and asks ps and lsof where there is none, as on macOS. An error
// says some or all processes could not be read, which is not the same as
// none running. A worktree that is gone holds nothing.
func lingeringProcesses(dir string) ([]string, error) {
	root, err := filepath.EvalSymlinks(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	read := procProcesses
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		read = psProcesses
	}
	procs, err := read()
	if err != nil {
		return nil, err
	}
	return lingering(root, os.Getpid(), procs)
}

// process is what lingering weighs of a process: its parent, its
// controlling terminal ("" for none), its command name, and its working
// directory ("" where it cannot be read). Where cwdPart is set, cwd holds
// only the start of the working directory, as far as lsofName could read it
// for certain.
type process struct {
	ppid    int
	tty     string
	comm    string
	cwd     string
	cwdPart bool
}

// lingering names the commands in procs running with a working directory at
// or under root, other than self, its ancestors, and processes attached to a
// terminal that is not self's. A process whose working directory may or may
// not be under root is an error, which says the processes could not all be
// read; the commands known to be under root are named beside it.
func lingering(root string, self int, procs map[int]process) ([]string, error) {
	skip := map[int]bool{}
	for pid := self; pid > 1 && !skip[pid]; pid = procs[pid].ppid {
		skip[pid] = true
	}
	ownTTY := procs[self].tty
	under := root + string(filepath.Separator)
	seen := map[string]bool{}
	var names, unclear []string
	for pid, p := range procs {
		if skip[pid] || p.comm == "" {
			continue
		}
		if p.tty != "" && p.tty != ownTTY {
			continue
		}
		inside := strings.HasPrefix(p.cwd, under) || !p.cwdPart && p.cwd == root
		if !inside {
			// The start of a directory that could still continue into
			// root is unknown; any other is outside it.
			if p.cwdPart && strings.HasPrefix(under, p.cwd) {
				unclear = append(unclear, fmt.Sprintf("%s (pid %d)", p.comm, pid))
			}
			continue
		}
		if seen[p.comm] {
			continue
		}
		seen[p.comm] = true
		names = append(names, p.comm)
	}
	sort.Strings(names)
	if len(unclear) > 0 {
		sort.Strings(unclear)
		return names, fmt.Errorf("lsof's listing leaves unclear whether these work under %s: %s", root, strings.Join(unclear, ", "))
	}
	return names, nil
}

// procProcesses reads every process in /proc.
func procProcesses() (map[int]process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	procs := map[int]process{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		ppid, tty, ok := parseProcStat(string(data))
		if !ok {
			continue
		}
		cwd, _ := os.Readlink(filepath.Join("/proc", e.Name(), "cwd"))
		comm, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		procs[pid] = process{ppid: ppid, tty: tty, comm: strings.TrimSpace(string(comm)), cwd: cwd}
	}
	return procs, nil
}

// parseProcStat returns a process's parent PID and controlling terminal
// ("" for none) from its /proc/<pid>/stat line.
func parseProcStat(s string) (ppid int, tty string, ok bool) {
	// The command name may contain spaces and parentheses; fields resume
	// after the last ')': state, ppid, pgrp, session, tty_nr, ...
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	if len(f) < 5 {
		return 0, "", false
	}
	ppid, _ = strconv.Atoi(f[1])
	if f[4] != "0" {
		tty = f[4]
	}
	return ppid, tty, true
}

// psProcesses asks ps for every process's parent, terminal and command name,
// and lsof for the working directories it can read. Each -o takes one
// keyword: BSD ps reads the rest of an argument after "=" as the header.
func psProcesses() (map[int]process, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=", "-o", "tty=", "-o", "ucomm=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	// lsof exits 1 when some process's directory could not be read, and
	// still lists the rest. It lists this process's own directory, so a run
	// that lists nothing failed, as does one that ends any other way. In the
	// C locale it prints every byte outside printable ASCII as an escape, so
	// what it prints does not hang on the locale's idea of printable.
	lsof := exec.Command("lsof", "-w", "-n", "-P", "-d", "cwd", "-F", "pn")
	lsof.Env = append(os.Environ(), "LC_ALL=C")
	cwds, err := lsof.Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1 && len(cwds) > 0) {
		return nil, fmt.Errorf("lsof: %w", err)
	}
	return fromPSAndLsof(string(out), string(cwds)), nil
}

// fromPSAndLsof reads ps's pid, ppid, tty and ucomm columns, and lsof's -F
// pn listing of cwd descriptors, in which a p line names a process and the
// n line after it its working directory, escaped as lsofName reads it. A
// terminal of "?" (Linux) or "??" (macOS) is none.
func fromPSAndLsof(ps, lsof string) map[int]process {
	procs := map[int]process{}
	for _, line := range strings.Split(ps, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		tty := f[2]
		if strings.Trim(tty, "?") == "" {
			tty = ""
		}
		procs[pid] = process{ppid: ppid, tty: tty, comm: strings.Join(f[3:], " ")}
	}
	pid := 0
	for _, line := range strings.Split(lsof, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if p, ok := procs[pid]; ok {
				p.cwd, p.cwdPart = lsofName(line[1:])
				procs[pid] = p
			}
		}
	}
	return procs
}

// lsofName decodes a name as lsof prints it: \\ for a backslash; \b, \f, \n,
// \r and \t; ^ and the character 0x40 above it for any other control (^A for
// 0x01); ^? for 0xff where char is unsigned; and \xNN for any other byte the
// locale cannot print (safepup and safestrprt in lsof's lib/misc.c, alike in
// the 4.91 macOS ships and in 4.99). lsof prints ^ as itself, so ^A may be a
// control or ^ and A, and an escape lsof never prints has no reading. The
// name stops at the first of either: part is set, and name holds what
// precedes it.
func lsofName(s string) (name string, part bool) {
	escaped := map[byte]byte{'\\': '\\', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '^' && i+1 < len(s) && caretControl(s[i+1]) {
			return b.String(), true
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 == len(s) {
			return b.String(), true
		}
		i++
		if e, ok := escaped[s[i]]; ok {
			b.WriteByte(e)
			continue
		}
		if s[i] != 'x' || i+3 > len(s) {
			return b.String(), true
		}
		v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
		if err != nil {
			return b.String(), true
		}
		b.WriteByte(byte(v))
		i += 2
	}
	return b.String(), false
}

// caretControl says whether lsof prints a control as ^ and c. ^H, ^I, ^J, ^L
// and ^M are not among them: those controls print as \b, \t, \n, \f and \r.
func caretControl(c byte) bool {
	return c == '?' || c > '@' && c <= '_' && !strings.ContainsRune("HIJLM", rune(c))
}
