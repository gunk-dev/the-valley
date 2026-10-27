package export

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// decodeReader bounds the work between cancellation checks while a token
// spans several reads. Closing a blocked source is the caller's responsibility.
type decodeReader struct {
	ctx context.Context
	r   io.Reader
}

func (r decodeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p[:min(len(p), 4096)])
	if stopped := r.ctx.Err(); stopped != nil {
		return 0, stopped
	}
	return n, err
}

// decodeValue consumes one value at a time so decoding a collection checks
// cancellation between its members. The export schema uses structs, slices,
// pointers and scalar values.
func decodeValue(ctx context.Context, dec *tokenDecoder, v reflect.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	if token == nil {
		if v.Kind() == reflect.Pointer || v.Kind() == reflect.Slice {
			v.SetZero()
		}
		return nil
	}
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		if token != json.Delim('{') {
			break
		}
		for dec.More() {
			if err := ctx.Err(); err != nil {
				return err
			}
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("expected an object field")
			}
			// exactjson.Check has passed the document, so a key names
			// its field exactly.
			field := -1
			for i := 0; i < v.NumField(); i++ {
				if tag, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ","); tag == name {
					field = i
					break
				}
			}
			if field < 0 {
				return fmt.Errorf("json: unknown field %q", name)
			}
			if err := decodeValue(ctx, dec, v.Field(field)); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case reflect.Slice:
		if token != json.Delim('[') {
			break
		}
		v.Set(reflect.MakeSlice(v.Type(), 0, 0))
		for dec.More() {
			item := reflect.New(v.Type().Elem()).Elem()
			if err := decodeValue(ctx, dec, item); err != nil {
				return err
			}
			v.Set(reflect.Append(v, item))
		}
		_, err := dec.Token()
		return err
	case reflect.String:
		if s, ok := token.(string); ok {
			v.SetString(s)
			return ctx.Err()
		}
	case reflect.Bool:
		if b, ok := token.(bool); ok {
			v.SetBool(b)
			return nil
		}
	case reflect.Int, reflect.Int64:
		if n, ok := token.(json.Number); ok {
			i, err := n.Int64()
			if err != nil {
				return err
			}
			if v.OverflowInt(i) {
				return fmt.Errorf("integer overflows %s", v.Type())
			}
			v.SetInt(i)
			return nil
		}
	}
	return fmt.Errorf("cannot decode %T into %s", token, v.Type())
}

// tokenDecoder leaves JSON grammar and scalar types to encoding/json.
// stringInput replaces strings with empty strings before that decoder sees
// them. Their values are scanned separately with cancellation checks.
type tokenDecoder struct {
	*json.Decoder
	input *stringInput
}

func newTokenDecoder(ctx context.Context, r io.Reader) *tokenDecoder {
	input := &stringInput{ctx: ctx, r: bufio.NewReaderSize(decodeReader{ctx, r}, 4096)}
	dec := json.NewDecoder(input)
	dec.UseNumber()
	return &tokenDecoder{Decoder: dec, input: input}
}

func (d *tokenDecoder) Token() (json.Token, error) {
	token, err := d.Decoder.Token()
	if err != nil {
		return nil, err
	}
	if _, ok := token.(string); ok {
		token = d.input.values[0]
		d.input.values[0] = ""
		d.input.values = d.input.values[1:]
	}
	return token, d.input.ctx.Err()
}

type stringInput struct {
	ctx     context.Context
	r       *bufio.Reader
	values  []string
	pending bool
	err     error
}

func (s *stringInput) Read(p []byte) (n int, err error) {
	// More can inspect input without reporting its error. Keep that error
	// for the next token read so malformed strings cannot disappear.
	if s.err != nil {
		return 0, s.err
	}
	defer func() {
		if err != nil {
			s.err = err
		}
	}()
	if len(p) == 0 {
		return 0, nil
	}
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if s.pending {
		p[0] = '"'
		s.pending = false
		return 1, nil
	}
	b, err := s.r.ReadByte()
	if err != nil {
		return 0, err
	}
	if b != '"' {
		p[0] = b
		return 1, nil
	}
	value, err := s.readString()
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return 0, err
	}
	s.values = append(s.values, value)
	p[0] = '"'
	s.pending = true
	return 1, nil
}

// readString decodes one string in small pieces. Joining those pieces checks
// cancellation between copies, including after the source's final read.
func (s *stringInput) readString() (string, error) {
	var parts []string
	var part strings.Builder
	size := 0
	for {
		if err := s.ctx.Err(); err != nil {
			return "", err
		}
		r, _, err := s.r.ReadRune()
		if err != nil {
			return "", err
		}
		if r == '"' {
			break
		}
		if r < 0x20 {
			return "", fmt.Errorf("control character in JSON string")
		}
		if r == '\\' {
			b, err := s.r.ReadByte()
			if err != nil {
				return "", err
			}
			switch b {
			case '"', '\\', '/':
				r = rune(b)
			case 'b':
				r = '\b'
			case 'f':
				r = '\f'
			case 'n':
				r = '\n'
			case 'r':
				r = '\r'
			case 't':
				r = '\t'
			case 'u':
				r, err = s.hexRune()
				if err != nil {
					return "", err
				}
				if utf16.IsSurrogate(r) {
					if r >= 0xD800 && r <= 0xDBFF {
						next, _ := s.r.Peek(6)
						if len(next) == 6 && next[0] == '\\' && next[1] == 'u' {
							low, ok := hexValue(next[2:])
							if ok && low >= 0xDC00 && low <= 0xDFFF {
								_, _ = s.r.Discard(6)
								r = utf16.DecodeRune(r, low)
							} else {
								r = utf8.RuneError
							}
						} else {
							r = utf8.RuneError
						}
					} else {
						r = utf8.RuneError
					}
				}
			default:
				return "", fmt.Errorf("invalid escape in JSON string")
			}
		}
		part.WriteRune(r)
		if part.Len() >= 4096 {
			parts = append(parts, part.String())
			size += part.Len()
			part = strings.Builder{}
		}
	}
	parts = append(parts, part.String())
	size += part.Len()
	if err := s.ctx.Err(); err != nil {
		return "", err
	}
	var out strings.Builder
	out.Grow(size)
	for _, p := range parts {
		if err := s.ctx.Err(); err != nil {
			return "", err
		}
		out.WriteString(p)
	}
	return out.String(), s.ctx.Err()
}

func (s *stringInput) hexRune() (rune, error) {
	var b [4]byte
	if _, err := io.ReadFull(s.r, b[:]); err != nil {
		return 0, err
	}
	r, ok := hexValue(b[:])
	if !ok {
		return 0, fmt.Errorf("invalid Unicode escape in JSON string")
	}
	return r, nil
}

func hexValue(b []byte) (rune, bool) {
	var r rune
	for _, c := range b {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r += rune(c - '0')
		case c >= 'a' && c <= 'f':
			r += rune(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			r += rune(c - 'A' + 10)
		default:
			return 0, false
		}
	}
	return r, true
}
