package sigverify

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// AllowedSigners is a parsed allowed-signers file, in the format
// ssh-keygen(1) describes under ALLOWED SIGNERS. Each line is
//
//	principals [options] keytype base64-key [comment]
//
// The principals field is a comma-separated pattern list. The options are
// the four OpenSSH defines: cert-authority, namespaces="list",
// valid-after="time" and valid-before="time". This package adds two:
//
//   - verify-required demands the user-verification bit on every signature
//     by that line's key. It is the same keyword sshd accepts in
//     authorized_keys.
//   - tkey-signer, or tkey-signer="version", marks the line's key as a
//     Tillitis TKey running the touch-requiring signer app. The version is
//     bookkeeping: it is reported, and never compared with anything.
//
// Every line has a signer class. A security key is fido-sk. An ssh-ed25519
// key on a tkey-signer line is tkey-signer. Any other key is software. A
// caller names the classes it accepts, and a line of any other class never
// authorizes a signature.
//
// A line carrying no-touch-required is an error. User presence is not
// negotiable here, so a file that asks to waive it is refused whole rather
// than half-honoured. Any other unknown option, a malformed line, or a key
// type this package does not read is an error too: ssh-keygen skips a line
// it cannot parse, and this refuses the file instead, so a typo cannot
// silently drop a signer's restrictions.
type AllowedSigners struct {
	source string
	lines  []signerLine
}

type signerLine struct {
	number     int
	principals string

	certAuthority bool
	// hasNamespaces separates an absent namespaces option from
	// namespaces="", which permits nothing.
	hasNamespaces  bool
	namespaces     string
	validAfter     int64 // Unix seconds; 0 when absent
	validBefore    int64
	verifyRequired bool
	tkeySigner     bool
	signerApp      string // the tkey-signer value, if any

	key   []byte // the public key's wire encoding
	class Class
}

// LoadAllowedSigners reads and parses an allowed-signers file.
func LoadAllowedSigners(path string) (*AllowedSigners, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseAllowedSigners(path, data)
}

// ParseAllowedSigners parses allowed-signers lines. source names them in
// errors and refusals, usually the file's path.
func ParseAllowedSigners(source string, data []byte) (*AllowedSigners, error) {
	a := &AllowedSigners{source: source}
	for i, raw := range strings.Split(string(data), "\n") {
		// OpenSSH reads each line as a C string, which ends at the first
		// NUL. A line holding one means something different to ssh-keygen
		// than to this parser: "*,!release<NUL>x K" is a line ssh-keygen
		// rejects, where this would read an exclusion that excludes
		// nothing. A NUL anywhere refuses the file.
		if strings.IndexByte(raw, 0) >= 0 {
			return nil, fmt.Errorf("%s:%d: the line holds a NUL byte", source, i+1)
		}
		line := strings.TrimLeft(raw, " \t\r\n")
		if line == "" || line[0] == '#' {
			continue
		}
		parsed, err := parseSignerLine(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", source, i+1, err)
		}
		parsed.number = i + 1
		a.lines = append(a.lines, parsed)
	}
	return a, nil
}

func parseSignerLine(line string) (signerLine, error) {
	var l signerLine
	principals, rest, err := principalsField(line)
	if err != nil {
		return l, err
	}
	l.principals = principals

	// The options field is optional, so the next word is either a key type
	// or the options.
	word, _, _ := strings.Cut(rest, " ")
	word, _, _ = strings.Cut(word, "\t")
	if !knownKeyType(word) && looksLikeKeyType(word) {
		return l, fmt.Errorf("keys of type %q are not supported", word)
	}
	if !knownKeyType(word) {
		opts, after, err := optionsField(rest)
		if err != nil {
			return l, err
		}
		if err := l.parseOptions(opts); err != nil {
			return l, err
		}
		rest = after
	}

	var keyType string
	if l.key, keyType, err = keyField(rest); err != nil {
		return l, err
	}
	switch {
	case keyType == TypeSKEd25519 || keyType == TypeSKECDSA:
		if l.tkeySigner {
			return l, fmt.Errorf("tkey-signer marks a TKey's ssh-ed25519 key, and this is a %s key", keyType)
		}
		l.class = ClassFIDO
	case l.tkeySigner:
		if keyType != TypeEd25519 {
			return l, fmt.Errorf("tkey-signer marks a TKey's ssh-ed25519 key, and this is a %s key", keyType)
		}
		if l.verifyRequired {
			return l, errors.New("verify-required cannot hold for a tkey-signer line: a TKey signature cannot prove user verification")
		}
		if l.certAuthority {
			return l, errors.New("a tkey-signer line cannot be a cert-authority")
		}
		l.class = ClassTKey
	default:
		l.class = ClassSoftware
	}
	return l, nil
}

// principalsField splits off the first field the way OpenSSH's strdelimw
// does: up to whitespace, or a double-quoted string that may hold spaces.
func principalsField(line string) (field, rest string, err error) {
	i := strings.IndexAny(line, " \t\r\n\"")
	if i < 0 {
		return "", "", errors.New("the line has principals and no key")
	}
	if line[i] == '"' {
		j := strings.IndexByte(line[i+1:], '"')
		if j < 0 {
			return "", "", errors.New("the principals field has no closing quote")
		}
		field = line[:i] + line[i+1:i+1+j]
		rest = line[i+1+j+1:]
	} else {
		field, rest = line[:i], line[i+1:]
	}
	if field == "" {
		return "", "", errors.New("the principals field is empty")
	}
	return field, strings.TrimLeft(rest, " \t\r\n"), nil
}

// optionsField splits off the options: everything up to the first space or
// tab outside double quotes, where \" does not end a quote.
func optionsField(rest string) (opts, after string, err error) {
	quoted := false
	i := 0
	for ; i < len(rest) && (quoted || (rest[i] != ' ' && rest[i] != '\t')); i++ {
		switch {
		case rest[i] == '\\' && i+1 < len(rest) && rest[i+1] == '"':
			i++
		case rest[i] == '"':
			quoted = !quoted
		}
	}
	if quoted {
		return "", "", errors.New("the options have an unclosed quote")
	}
	after = strings.TrimLeft(rest[i:], " \t")
	if after == "" {
		return "", "", errors.New("the line has no key")
	}
	return rest[:i], after, nil
}

// parseOptions reads comma-separated options, as sshsigopt_parse does.
// Keywords are case-insensitive, and every value is double-quoted.
func (l *signerLine) parseOptions(opts string) error {
	seen := map[string]bool{}
	for opts != "" {
		name := opts
		if i := strings.IndexAny(opts, "=,"); i >= 0 {
			name = opts[:i]
		}
		opts = opts[len(name):]
		name = strings.ToLower(name)

		var value string
		hasValue := strings.HasPrefix(opts, "=")
		if hasValue {
			var err error
			if value, opts, err = dequote(opts[1:]); err != nil {
				return fmt.Errorf("option %s: %w", name, err)
			}
		}
		if seen[name] {
			return fmt.Errorf("option %s appears twice", name)
		}
		seen[name] = true

		switch {
		case name == "cert-authority" && !hasValue:
			l.certAuthority = true
		case name == "verify-required" && !hasValue:
			l.verifyRequired = true
		case name == "tkey-signer":
			if hasValue && !validSignerApp(value) {
				return fmt.Errorf("tkey-signer=%q is not a version: use 1 to 64 letters, digits and . _ + : -", value)
			}
			l.tkeySigner, l.signerApp = true, value
		case name == "namespaces" && hasValue:
			l.hasNamespaces, l.namespaces = true, value
		case name == "valid-after" && hasValue:
			t, err := ParseTime(value)
			if err != nil {
				return fmt.Errorf("valid-after: %w", err)
			}
			l.validAfter = t.Unix()
		case name == "valid-before" && hasValue:
			t, err := ParseTime(value)
			if err != nil {
				return fmt.Errorf("valid-before: %w", err)
			}
			l.validBefore = t.Unix()
		case name == "no-touch-required":
			return errors.New("no-touch-required is refused: this verifier always requires user presence")
		default:
			return fmt.Errorf("unknown option %q", name)
		}

		if opts == "" {
			break
		}
		if opts[0] != ',' {
			return fmt.Errorf("option %s is followed by %q where a comma belongs", name, opts[0])
		}
		if opts = opts[1:]; opts == "" {
			return errors.New("the options end with a comma")
		}
	}
	if l.validAfter != 0 && l.validBefore != 0 && l.validBefore <= l.validAfter {
		return errors.New("valid-before is not after valid-after")
	}
	return nil
}

// dequote reads a double-quoted value, where \" stands for a quote, and
// returns what follows the closing quote.
func dequote(s string) (value, rest string, err error) {
	if !strings.HasPrefix(s, `"`) {
		return "", "", errors.New("the value is not in double quotes")
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == '"':
			b.WriteByte('"')
			i++
		case s[i] == '"':
			return b.String(), s[i+1:], nil
		default:
			b.WriteByte(s[i])
		}
	}
	return "", "", errors.New("the value has no closing quote")
}

// validSignerApp holds a tkey-signer version to a plain token.
func validSignerApp(v string) bool {
	if len(v) == 0 || len(v) > 64 {
		return false
	}
	for _, c := range v {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._+:-", c)
		if !ok {
			return false
		}
	}
	return true
}

// keyField reads "keytype base64 [comment]" and returns the key's wire
// encoding and type.
func keyField(rest string) ([]byte, string, error) {
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return nil, "", errors.New("the line has no key")
	}
	typ := fields[0]
	if !knownKeyType(typ) {
		return nil, "", fmt.Errorf("keys of type %q are not supported", typ)
	}
	blob, err := base64.StdEncoding.Strict().DecodeString(fields[1])
	if err != nil {
		return nil, "", fmt.Errorf("the %s key is not canonical base64: %w", typ, err)
	}
	key, err := parsePublicKey(blob)
	if err != nil {
		return nil, "", err
	}
	if key.typ != typ {
		return nil, "", fmt.Errorf("the key is labelled %s but holds a %s key", typ, key.typ)
	}
	return blob, typ, nil
}

// looksLikeKeyType catches OpenSSH key types this package does not read,
// such as ssh-dss or a certificate type, so the error names the key rather
// than calling it an unknown option.
func looksLikeKeyType(word string) bool {
	if strings.ContainsAny(word, "=,") {
		return false
	}
	for _, prefix := range []string{"ssh-", "sk-", "ecdsa-", "webauthn-", "rsa-"} {
		if strings.HasPrefix(word, prefix) {
			return true
		}
	}
	return strings.HasSuffix(word, "@openssh.com")
}

func knownKeyType(word string) bool {
	switch word {
	case TypeSKEd25519, TypeSKECDSA, TypeEd25519, TypeECDSA256, TypeECDSA384, TypeECDSA521, TypeRSA:
		return true
	}
	return false
}

// ParseTime reads a time in the form allowed-signers options and
// `ssh-keygen -O verify-time=` take: YYYYMMDD, YYYYMMDDHHMM or
// YYYYMMDDHHMMSS, in UTC when followed by Z or UTC and in local time
// otherwise.
func ParseTime(s string) (time.Time, error) {
	loc, digits := time.Local, s
	if t, ok := strings.CutSuffix(strings.ToUpper(s), "UTC"); ok && len(s) > 3 {
		loc, digits = time.UTC, s[:len(t)]
	} else if t, ok := strings.CutSuffix(strings.ToUpper(s), "Z"); ok && len(s) > 1 {
		loc, digits = time.UTC, s[:len(t)]
	}
	layouts := map[int]string{8: "20060102", 12: "200601021504", 14: "20060102150405"}
	layout, ok := layouts[len(digits)]
	if !ok {
		return time.Time{}, fmt.Errorf("%q is not YYYYMMDD[HHMM[SS]][Z]", s)
	}
	t, err := time.ParseInLocation(layout, digits, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not YYYYMMDD[HHMM[SS]][Z]", s)
	}
	if t.Unix() <= 0 {
		return time.Time{}, fmt.Errorf("%q is not after 1970", s)
	}
	return t, nil
}

// request is what lookup matches lines against.
type request struct {
	key       []byte
	principal string // empty only when any principal will do
	namespace string
	at        time.Time
	classes   map[Class]bool
}

// match is what the lines that matched a request say about it.
type match struct {
	principal  string
	class      Class
	signerApp  string
	uvRequired bool
}

// lookup finds the lines that let a key sign. It mirrors
// sshsig_check_allowed_keys, and adds the signer class. A line matches
// when:
//
//   - its key is the request's key
//   - its class is one the request accepts
//   - its principals pattern list matches the principal, unless any
//     principal will do
//   - its namespaces, if present, match the namespace
//   - the request's time falls in its validity window
//
// Certificate-authority lines never match, because this package does not
// verify certificates.
//
// With no match, lookup returns the most specific refusal any line
// produced. With a match, it reports the first matching line's class and
// signer app, and the principal: the requested one, or with any principal
// that line's principals field. UV is required if any matching line
// carries verify-required, so the strictest line holds.
func (a *AllowedSigners) lookup(req request) (*match, *Refusal) {
	refusal := &Refusal{Reason: UnknownKey, Detail: fmt.Sprintf("no line in %s lists this key", a.source)}
	rank := 0
	note := func(r int, reason Reason, detail string) {
		if r > rank {
			rank, refusal = r, &Refusal{Reason: reason, Detail: detail}
		}
	}
	now := req.at.Unix()
	var m *match
	for _, l := range a.lines {
		if l.certAuthority || !bytes.Equal(l.key, req.key) {
			continue
		}
		where := fmt.Sprintf("%s:%d", a.source, l.number)
		switch {
		case !req.classes[l.class]:
			note(1, ClassNotAllowed, fmt.Sprintf("%s lists this key as a %s signer, and this call accepts only %s", where, l.class, classList(req.classes)))
		case req.principal != "" && matchPatternList(req.principal, l.principals) != 1:
			note(2, PrincipalNotAllowed, fmt.Sprintf("%s lists this key for %q, which does not match %q", where, l.principals, req.principal))
		case l.hasNamespaces && matchPatternList(req.namespace, l.namespaces) != 1:
			note(3, NamespaceNotAllowed, fmt.Sprintf("%s allows this key only in namespaces %q, which do not match %q", where, l.namespaces, req.namespace))
		case l.validAfter != 0 && now < l.validAfter:
			note(4, NotYetValid, fmt.Sprintf("%s makes this key valid only from %s, and the verify time is %s", where, stamp(l.validAfter), stamp(now)))
		case l.validBefore != 0 && now > l.validBefore:
			note(4, Expired, fmt.Sprintf("%s made this key valid only until %s, and the verify time is %s", where, stamp(l.validBefore), stamp(now)))
		default:
			if m == nil {
				m = &match{principal: req.principal, class: l.class, signerApp: l.signerApp}
				if m.principal == "" {
					m.principal = l.principals
				}
			}
			m.uvRequired = m.uvRequired || l.verifyRequired
		}
	}
	if m == nil {
		return nil, refusal
	}
	return m, nil
}

func stamp(unix int64) string {
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

// matchPatternList is OpenSSH's match_pattern_list, transliterated. It
// returns 1 when s matches a pattern in the comma-separated list, -1 when
// it matches a pattern negated with "!", which overrides any positive
// match, and 0 otherwise. Matching is case-sensitive. As in OpenSSH, a
// pattern of 1023 bytes or more makes the whole list match nothing.
func matchPatternList(s, list string) int {
	const maxPattern = 1024 - 1 // OpenSSH's sub[1024] buffer, less its NUL
	positive := 0
	for i := 0; i < len(list); {
		negated := list[i] == '!'
		if negated {
			i++
		}
		j := i
		for j < len(list) && j-i < maxPattern && list[j] != ',' {
			j++
		}
		if j-i >= maxPattern {
			return 0
		}
		pattern := list[i:j]
		i = j
		if i < len(list) && list[i] == ',' {
			i++
		}
		if matchPattern(s, pattern) {
			if negated {
				return -1
			}
			positive = 1
		}
	}
	return positive
}

// matchPattern is OpenSSH's match_pattern, transliterated: "*" matches any
// run of bytes, "?" matches one byte, and every other byte matches itself.
// A "*" or "?" in the pattern is always a wildcard, even where s holds the
// same byte.
func matchPattern(s, pattern string) bool {
	for {
		if pattern == "" {
			return s == ""
		}
		if pattern[0] == '*' {
			pattern = strings.TrimLeft(pattern, "*")
			if pattern == "" {
				return true
			}
			if pattern[0] != '?' {
				for i := 0; i < len(s); i++ {
					if s[i] == pattern[0] && matchPattern(s[i+1:], pattern[1:]) {
						return true
					}
				}
				return false
			}
			for i := 0; i < len(s); i++ {
				if matchPattern(s[i:], pattern) {
					return true
				}
			}
			return false
		}
		if s == "" {
			return false
		}
		if pattern[0] != '?' && pattern[0] != s[0] {
			return false
		}
		s, pattern = s[1:], pattern[1:]
	}
}
