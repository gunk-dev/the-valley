package sigverify

import (
	"encoding/binary"
	"errors"
	"math/big"
)

var errShort = errors.New("ends early")

// reader reads the SSH wire encoding of RFC 4251 section 5. A string is a
// four-byte big-endian length followed by that many bytes.
//
// The first failed read records an error. Every later read then returns a
// zero value, so a parser reads all of its fields and checks err once.
type reader struct {
	rest []byte
	err  error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || len(r.rest) < n {
		r.err = errShort
		return nil
	}
	b := r.rest[:n]
	r.rest = r.rest[n:]
	return b
}

func (r *reader) u8() byte {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (r *reader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func (r *reader) str() []byte {
	n := r.u32()
	if r.err != nil {
		return nil
	}
	if uint64(n) > uint64(len(r.rest)) {
		r.err = errShort
		return nil
	}
	return r.take(int(n))
}

// mpint reads a non-negative multiple-precision integer. OpenSSH refuses
// negative values, and so does this.
func (r *reader) mpint() *big.Int {
	b := r.str()
	if r.err != nil {
		return nil
	}
	if len(b) > 0 && b[0]&0x80 != 0 {
		r.err = errors.New("holds a negative integer")
		return nil
	}
	return new(big.Int).SetBytes(b)
}

// end reports the first failure, or trailing bytes that nothing read.
func (r *reader) end() error {
	if r.err != nil {
		return r.err
	}
	if len(r.rest) != 0 {
		return errors.New("has trailing bytes")
	}
	return nil
}

// appendString appends b in the wire encoding of a string.
func appendString(dst, b []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(b)))
	return append(dst, b...)
}
