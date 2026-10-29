package export

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// MinToken and MaxToken bound the length of the receiver's token, in
// bytes.
const (
	MinToken = 32
	MaxToken = 256
)

// ReadToken reads the receiver's token from the file at path. The file
// holds one line: MinToken to MaxToken visible ASCII characters, with a
// newline after them or none. The publisher and the receiver read it with
// the same rules. An error never quotes the file.
func ReadToken(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxToken+2))
	if err != nil {
		return "", err
	}
	token := strings.TrimSuffix(string(data), "\n")
	if len(token) < MinToken || len(token) > MaxToken {
		return "", fmt.Errorf("the token in %s is not %d to %d characters on one line", path, MinToken, MaxToken)
	}
	for i := 0; i < len(token); i++ {
		if token[i] < 0x21 || token[i] > 0x7e {
			return "", fmt.Errorf("the token in %s holds a character that is not visible ASCII", path)
		}
	}
	return token, nil
}

// BearerHeader is the value of the Authorization header that carries the
// token on every request of an upload.
func BearerHeader(token string) string { return "Bearer " + token }

// CarriesToken reports whether the request carries the token in exactly one
// Authorization header. It compares SHA-256 sums in constant time, so the
// time it takes says nothing about how much of the token a request got
// right, or how long the token is.
func CarriesToken(r *http.Request, token string) bool {
	got := r.Header.Values("Authorization")
	if len(got) != 1 {
		return false
	}
	a := sha256.Sum256([]byte(got[0]))
	b := sha256.Sum256([]byte(BearerHeader(token)))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}
