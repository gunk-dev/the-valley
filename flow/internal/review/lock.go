package review

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"the-valley/flow/internal/location"
)

// ErrLocked: another klaus review --post holds this PR's lock.
var ErrLocked = errors.New("another valley flow review --post is running for this PR")

// lockPR takes a non-blocking exclusive flock on review-<owner>-<repo>-<n>.lock
// in each of dirs, in order; the returned func releases it.
func lockPR(dirs []string, repo, prNumber string) (func(), error) {
	name := strings.NewReplacer("/", "-", string(filepath.Separator), "-").Replace("review-" + repo + "-" + prNumber + ".lock")
	var paths []string
	for _, dir := range dirs {
		paths = append(paths, filepath.Join(dir, name))
	}
	l, err := location.TryLock(paths...)
	var busy *location.BusyError
	if errors.As(err, &busy) {
		return nil, fmt.Errorf("%w (lock %s)", ErrLocked, busy.Path)
	}
	if err != nil {
		return nil, err
	}
	return l.Release, nil
}
