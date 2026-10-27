package export_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"the-valley/web/export"
)

func putBody(body io.Reader) *http.Request {
	return httptest.NewRequest("PUT", "/", body)
}

// untouched is a body that records whether anything read it.
type untouched struct{ read bool }

func (u *untouched) Read(p []byte) (int, error) {
	u.read = true
	return 0, io.EOF
}

// A body is kept under its hash once its size and hash match. Every
// refusal leaves the staging directory as it was.
func TestStageBody(t *testing.T) {
	data := []byte("the body's bytes\n")
	want := export.BodyOf("export.json", data)
	const limit = 1 << 20

	t.Run("a body that verifies", func(t *testing.T) {
		dir := t.TempDir()
		path, err := export.StageBody(dir, want, limit, putBody(bytes.NewReader(data)))
		if err != nil {
			t.Fatal(err)
		}
		if path != filepath.Join(dir, want.SHA256) {
			t.Errorf("staged at %s", path)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, data) {
			t.Errorf("staged %q", got)
		}
		// Sent again, it is accepted without being read.
		again := &untouched{}
		if _, err := export.StageBody(dir, want, limit, putBody(again)); err != nil || again.read {
			t.Errorf("a staged body sent again: %v, read %v", err, again.read)
		}
		if left := entries(t, dir); len(left) != 1 {
			t.Errorf("the directory holds %v", left)
		}
	})

	t.Run("a body declared over the cap is not read", func(t *testing.T) {
		dir := t.TempDir()
		body := &untouched{}
		_, err := export.StageBody(dir, want, int64(len(data))-1, putBody(body))
		wantCode(t, err, export.CodeTooLarge)
		if body.read {
			t.Error("the body was read")
		}
	})

	refusals := []struct {
		name string
		want export.Body
		req  func() *http.Request
		code export.Code
	}{
		{"a Content-Encoding", want, func() *http.Request {
			r := putBody(bytes.NewReader(data))
			r.Header.Set("Content-Encoding", "gzip")
			return r
		}, export.CodeEncoding},
		{"Content-Encoding identity", want, func() *http.Request {
			r := putBody(bytes.NewReader(data))
			r.Header.Set("Content-Encoding", "identity")
			return r
		}, export.CodeEncoding},
		{"a Content-Length other than the declared size", want, func() *http.Request {
			r := putBody(bytes.NewReader(data))
			r.ContentLength = int64(len(data)) + 1
			return r
		}, export.CodeSizeMismatch},
		{"a short body", want, func() *http.Request {
			r := putBody(bytes.NewReader(data[:5]))
			r.ContentLength = -1
			return r
		}, export.CodeSizeMismatch},
		{"a body longer than declared", want, func() *http.Request {
			r := putBody(io.MultiReader(bytes.NewReader(data), endless{}))
			r.ContentLength = -1
			return r
		}, export.CodeSizeMismatch},
		{"other bytes of the same size", want, func() *http.Request {
			return putBody(bytes.NewReader(bytes.ToUpper(data)))
		}, export.CodeHashMismatch},
		{"a name that is no hash", export.Body{Name: "x", SHA256: "../../outside", Size: int64(len(data))}, func() *http.Request {
			return putBody(bytes.NewReader(data))
		}, export.CodeBadManifest},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			parent := t.TempDir()
			dir := filepath.Join(parent, "bodies")
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			_, err := export.StageBody(dir, c.want, limit, c.req())
			wantCode(t, err, c.code)
			if left := entries(t, dir); len(left) != 0 {
				t.Errorf("the refusal left %v in the staging directory", left)
			}
			if left := entries(t, parent); len(left) != 1 {
				t.Errorf("the refusal wrote %v beside the staging directory", left)
			}
		})
	}
}

func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}
