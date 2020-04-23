package bspatch

import "io"

type writeCounter struct {
	w io.Writer
	n int64
}

func newWriteCounter(w io.Writer) *writeCounter {
	return &writeCounter{w, 0}
}

func (wc *writeCounter) Write(p []byte) (int, error) {
	n, err := wc.w.Write(p)
	wc.n += int64(n)
	return n, err
}

func (wc writeCounter) Count() int64 {
	return wc.n
}
