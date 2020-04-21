package bspatch

import (
	"errors"
	"fmt"
	"io"
)

var ErrUnequalReads = errors.New("byteAdder.Read: did not read equal number of bytes from both readers")

type byteAddReader struct {
	r1   io.Reader
	r2   io.Reader
	addb []byte
}

func newByteAddReader(r1 io.Reader, r2 io.Reader) byteAddReader {
	return byteAddReader{
		r1:   r1,
		r2:   r2,
		addb: make([]byte, ReadBufferSize),
	}
}

// Read adds the bytes of the two readers and writes to using p as scratch space.
func (ba byteAddReader) Read(p []byte) (written int, err error) {
	chunk := min(len(p), ReadBufferSize)

	for written < len(p) {
		r1n, r1err := ba.r1.Read(ba.addb[:min(chunk, len(p)-written)])
		r2n, r2err := ba.r2.Read(p[written:min(written+chunk, len(p))])
		n := min(r1n, r2n)

		for i := 0; i < n; i++ {
			p[written+i] += ba.addb[i]
		}
		written += n

		switch {
		case r1n != r2n:
			err = ErrUnequalReads
			return
		case r1err == nil && r2err == nil:
			continue
		case isNilorEOF(r1err) && isNilorEOF(r2err):
			err = io.EOF
			return
		case !isNilorEOF(r1err) || !isNilorEOF(r2err):
			err = fmt.Errorf("%s, %s", r1err, r2err)
			return
		}
	}

	return
}

func isNilorEOF(err error) bool {
	return err == io.EOF || err == nil
}
