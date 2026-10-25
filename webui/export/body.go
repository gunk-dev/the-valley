package export

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// StageBody streams one upload body into dir, the upload's staging
// directory, and keeps it only once it verified. It refuses a body
// declared larger than limit before reading any of it, and a body sent
// with a Content-Encoding, or with a Content-Length other than its
// declared size. It hashes the body while it reads, reads at most one
// byte past the declared size, and writes nothing outside dir: a
// temporary file while it reads, renamed to the body's SHA-256 once the
// size and hash match. A body already staged under that name has
// verified, and is kept as it is.
//
// It returns the staged file's path. Every refusal is an *Error.
func StageBody(dir string, want Body, limit int64, req *http.Request) (string, error) {
	if len(want.SHA256) != 64 || !isHex(want.SHA256) {
		return "", refuse(CodeBadManifest, "%q is not a SHA-256 in lowercase hex", want.SHA256)
	}
	if want.Size > limit {
		return "", refuse(CodeTooLarge, "%s declares %d bytes, more than this receiver's cap of %d", want.Name, want.Size, limit)
	}
	if len(req.Header.Values("Content-Encoding")) > 0 {
		return "", refuse(CodeEncoding, "a body is sent as it is, with no Content-Encoding")
	}
	if req.ContentLength >= 0 && req.ContentLength != want.Size {
		return "", refuse(CodeSizeMismatch, "%s is sent with Content-Length %d; the manifest declares %d", want.Name, req.ContentLength, want.Size)
	}
	path := filepath.Join(dir, want.SHA256)
	if _, err := os.Lstat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	f, err := os.CreateTemp(dir, ".partial-")
	if err != nil {
		return "", err
	}
	kept := false
	defer func() {
		if !kept {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(req.Body, want.Size+1))
	if err != nil {
		return "", refuse(CodeUnreadable, "%s could not be read: %v", want.Name, err)
	}
	if n > want.Size {
		return "", refuse(CodeSizeMismatch, "%s is longer than the %d bytes the manifest declares", want.Name, want.Size)
	}
	if n < want.Size {
		return "", refuse(CodeSizeMismatch, "%s is %d bytes; the manifest declares %d", want.Name, n, want.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want.SHA256 {
		return "", refuse(CodeHashMismatch, "%s has SHA-256 %s; the manifest declares %s", want.Name, got, want.SHA256)
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	kept = true
	return path, nil
}
