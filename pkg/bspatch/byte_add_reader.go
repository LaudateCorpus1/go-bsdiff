package bspatch

import (
	"errors"
	"fmt"
	"io"
)

var ErrUnequalReads = errors.New("byteAdder.Read: did not read equal number of bytes from both readers")

type byteAddReader struct {
	r1  io.Reader
	r2  io.Reader
	tmp [1]byte
}

func newByteAddReader(r1 io.Reader, r2 io.Reader) byteAddReader {
	// Temp buffer for reading edge case.
	var t [1]byte
	return byteAddReader{
		r1:  r1,
		r2:  r2,
		tmp: t,
	}
}

// Read adds the bytes of the two readers and writes to using p as scratch space.
// It writes half of len(p) at a time to avoid using an internal buffer;
// it is up to the client to specify the buffer size.
func (ba byteAddReader) Read(p []byte) (int, error) {
	var n1, n2 int
	var err1, err2, err error

	if len(p) == 1 {
		n1, err1 = ba.r1.Read(ba.tmp[:])
		n2, err2 = ba.r2.Read(p)
		p[0] += ba.tmp[0]
	} else {
		limit := len(p) / 2
		n1, err1 = ba.r1.Read(p[:limit])
		n2, err2 = ba.r2.Read(p[limit : limit+n1])
		for i, b := range p[limit : limit+n2] {
			p[i] += b
		}
	}

	switch {
	case n1 != n2:
		err = ErrUnequalReads
	case err1 == nil && err2 == nil:
		err = nil
	case isNilorEOF(err1) && isNilorEOF(err2):
		err = io.EOF
	case !isNilorEOF(err1) || !isNilorEOF(err2):
		err = fmt.Errorf("%s, %s", err1, err2)
	}

	return n2, err
}

func isNilorEOF(err error) bool {
	return err == io.EOF || err == nil
}
