package exporttest

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// sysProcAttr puts a program in a session of its own, and has the kernel
// kill it when the test process dies.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Pdeathsig: syscall.SIGKILL}
}

// killSession kills every process in the session sid, and again, for up
// to a second, until none is left, since a process may start another as
// it is killed.
func killSession(sid int) {
	for range 100 {
		pids := session(sid)
		if len(pids) == 0 {
			return
		}
		for _, pid := range pids {
			syscall.Kill(pid, syscall.SIGKILL)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// session is the processes in the session sid that have not exited, as
// /proc lists them.
func session(sid int) []int {
	stats, _ := filepath.Glob("/proc/[0-9]*/stat")
	var pids []int
	for _, path := range stats {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // the process has gone
		}
		// The command's name, in parentheses, may hold any byte. The
		// fields after it are the state, the parent, the process group
		// and the session.
		i := bytes.LastIndexByte(data, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(data[i+1:]))
		if len(f) < 4 || f[0] == "Z" || f[0] == "X" || f[3] != strconv.Itoa(sid) {
			continue
		}
		if pid, err := strconv.Atoi(filepath.Base(filepath.Dir(path))); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// watchdog is a process that outlives the test process and kills what it
// left running. The test process tells it, on a pipe it alone holds open,
// of each session it starts, and of each it has emptied. When the test
// process dies, however it dies, the kernel closes the pipe, and the
// watchdog kills every process still in each session it was told of and
// not told was emptied. A session the test emptied is forgotten, so that
// a later session given the same ID is never the watchdog's to kill.
var watchdog struct {
	once sync.Once
	mu   sync.Mutex
	// w is the pipe's end the test process writes, or nil when the
	// watchdog did not start.
	w *os.File
}

// watchdogScript is the watchdog, a shell that reads "+ SID" for a
// session the test started and "- SID" for one it has emptied.
const watchdogScript = `
sessions=' '
while read -r op sid; do
	case $op in
	+) sessions="$sessions$sid " ;;
	-)
		left=' '
		for s in $sessions; do [ "$s" = "$sid" ] || left="$left$s "; done
		sessions=$left
		;;
	esac
done
[ "$sessions" = ' ' ] && exit 0
for pass in 1 2 3; do
	for stat in /proc/[0-9]*/stat; do
		read -r line < "$stat" || continue
		set -- ${line##*\) }
		case $1 in Z|X) continue ;; esac
		case $sessions in *" $4 "*)
			pid=${stat#/proc/}
			kill -9 "${pid%/stat}"
			;;
		esac
	done 2> /dev/null
done
`

// watch tells the watchdog of the session sid, starting the watchdog
// first if this is the test process's first session.
func watch(sid int) {
	watchdog.once.Do(startWatchdog)
	tell("+", sid)
}

// unwatch tells the watchdog that the session sid is empty.
func unwatch(sid int) { tell("-", sid) }

func tell(op string, sid int) {
	watchdog.mu.Lock()
	defer watchdog.mu.Unlock()
	if watchdog.w != nil {
		fmt.Fprintf(watchdog.w, "%s %d\n", op, sid)
	}
}

// startWatchdog starts the watchdog in a session of its own, with no
// death signal, so it outlives the test process. Its output is
// /dev/null, since go test waits until no process holds the test's output
// open. It is never waited for: it exits once the test process has.
func startWatchdog() {
	r, w, err := os.Pipe()
	if err != nil {
		return
	}
	cmd := exec.Command("/bin/sh", "-c", watchdogScript)
	cmd.Stdin = r
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	r.Close()
	if err != nil {
		w.Close()
		return
	}
	watchdog.w = w
}
