// * Copyright 2003-2005 Colin Percival
// * All rights reserved
// *
// * Redistribution and use in source and binary forms, with or without
// * modification, are permitted providing that the following conditions
// * are met:
// * 1. Redistributions of source code must retain the above copyright
// *    notice, this list of conditions and the following disclaimer.
// * 2. Redistributions in binary form must reproduce the above copyright
// *    notice, this list of conditions and the following disclaimer in the
// *    documentation and/or other materials provided with the distribution.
// *
// * THIS SOFTWARE IS PROVIDED BY THE AUTHOR ``AS IS'' AND ANY EXPRESS OR
// * IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
// * WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE
// * ARE DISCLAIMED.  IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY
// * DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// * DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS
// * OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
// * HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT,
// * STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING
// * IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE
// * POSSIBILITY OF SUCH DAMAGE.

// Package bspatch is a binary diff program using suffix sorting.
package bspatch

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/dsnet/compress/bzip2"
	"github.com/gabstv/go-bsdiff/pkg/util"
)

// Bytes applies a patch with the oldfile to create the newfile
func Bytes(oldfile, patch []byte) (newfile []byte, err error) {
	return patchb(oldfile, patch)
}

// Reader applies a BSDIFF4 patch (using oldbin and patchf) to create the newbin
func Reader(oldbin io.Reader, newbin io.Writer, patchf io.Reader) error {
	oldbs, err := ioutil.ReadAll(oldbin)
	if err != nil {
		return err
	}
	diffbytes, err := ioutil.ReadAll(patchf)
	if err != nil {
		return err
	}
	newbs, err := patchb(oldbs, diffbytes)
	if err != nil {
		return err
	}
	return util.PutWriter(newbin, newbs)
}

// File applies a BSDIFF4 patch (using oldfile and patchfile) to create the newfile
func File(oldfile, newfile, patchfile string) error {
	oldf, err := os.Open(oldfile)
	if err != nil {
		return fmt.Errorf("could not open oldfile '%v': %v", oldfile, err.Error())
	}
	defer oldf.Close()

	newf, err := os.Create(newfile)
	if err != nil {
		return fmt.Errorf("could not open or create newfile '%v': %v", newfile, err.Error())
	}

	patchbs, err := ioutil.ReadFile(patchfile)
	if err != nil {
		return fmt.Errorf("could not read patchfile '%v': %v", patchfile, err.Error())
	}

	newfw := bufio.NewWriterSize(newf, WriteBufferSize)
	err = patchStream(oldf, newfw, patchbs)

	if err != nil {
		newf.Close()
		os.Remove(newfile)
		return fmt.Errorf("bspatch: %v", err.Error())
	}

	newfw.Flush()
	newf.Close()
	return nil
}

// Variables for streaming
var (
	ErrUnequalReads = errors.New("byteAdder.Read: did not read equal number of bytes from both readers")
	WriteBufferSize = 4096
	ReadBufferSize  = 4096
)

type CorruptPatchError struct {
	Name string
}

func newCorruptPatchError(e string) CorruptPatchError {
	return CorruptPatchError{"corrupt patch: " + e}
}

// Functionally, consumers should handle a corrupt patch and a bz end error the same.
func newCorruptPatchBzEndError(read int64, expected int64, label string, prevErr error) CorruptPatchError {
	errmsg := fmt.Sprintf("corrupt patch or bz stream ended: %s read (%v/%v) ", label, read, expected)
	if prevErr != nil {
		errmsg += prevErr.Error()
	}
	return CorruptPatchError{errmsg}
}

func (e CorruptPatchError) Error() string {
	return e.Name
}

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

func patchStream(oldf io.ReadSeeker, newf io.Writer, patch []byte) error {
	HeaderLen := 32

	// Reused container vars
	var lenread int64
	var errmsg string
	header := make([]byte, HeaderLen)
	hdbuf := make([]byte, 8)
	ctrl := make([]int64, 3)
	// ctrl triple indices
	var x, y, z = 0, 1, 2
	cpBuf := make([]byte, WriteBufferSize)

	// Counter used for sanity checks
	newfwc := newWriteCounter(newf)

	//	File format:
	//		0	8	"BSDIFF40"
	//		8	8	X
	//		16	8	Y
	//		24	8	sizeof(newfile)
	//		32	X	bzip2(control block)
	//		32+X	Y	bzip2(diff block)
	//		32+X+Y	???	bzip2(extra block)
	//	with control block a set of triples (x,y,z) meaning "add x bytes
	//	from oldfile to x bytes from the diff block; copy y bytes from the
	//	extra block; seek forwards in oldfile by z bytes".

	// Read the patch header
	p := bytes.NewReader(patch)
	n, err := p.Read(header)

	if err != nil {
		return newCorruptPatchError(err.Error())
	}
	if n < HeaderLen {
		errmsg = fmt.Sprintf("short header read (n %v < %v)", n, HeaderLen)
		return newCorruptPatchError(errmsg)
	}

	// Check for appropriate magic
	if bytes.Compare(header[:8], []byte("BSDIFF40")) != 0 {
		return newCorruptPatchError("incorrect magic number (header BSDIFF40)")
	}

	// Read lengths from header
	bzctrllen := offtin(header[8:])
	bzdatalen := offtin(header[16:])
	newsize := offtin(header[24:])

	if bzctrllen < 0 || bzdatalen < 0 || newsize < 0 {
		errmsg = fmt.Sprintf("negative length block(s) read from header (bzctrllen %v bzdatalen %v newsize %v)", bzctrllen, bzdatalen, newsize)
		return newCorruptPatchError(errmsg)
	}

	// Close patch file and re-open it via libbzip2 at the right places
	p = nil
	cpfbz2 := bytes.NewReader(patch)
	if _, err := cpfbz2.Seek(int64(HeaderLen), io.SeekStart); err != nil {
		return err
	}
	cpf, err := bzip2.NewReader(cpfbz2, nil)
	if err != nil {
		return err
	}
	dpfbz2 := bytes.NewReader(patch)
	if _, err := dpfbz2.Seek(int64(HeaderLen)+bzctrllen, io.SeekStart); err != nil {
		return err
	}
	dpf, err := bzip2.NewReader(dpfbz2, nil)
	if err != nil {
		return err
	}
	epfbz2 := bytes.NewReader(patch)
	if _, err := epfbz2.Seek(int64(HeaderLen)+bzctrllen+bzdatalen, io.SeekStart); err != nil {
		return err
	}
	epf, err := bzip2.NewReader(epfbz2, nil)
	if err != nil {
		return err
	}

	xbyteadd := newByteAddReader(dpf, oldf)

	for newfwc.Count() < newsize {
		// Read control data
		for i := 0; i <= 2; i++ {
			lenread, err = zreadall(cpf, hdbuf, 8)
			if lenread != 8 || (err != nil && err != io.EOF) {
				// format "corrupt patch or bz stream ended: control data read (lenread/8) err.Error()"
				return newCorruptPatchBzEndError(lenread, 8, "control data", err)
			}
			ctrl[i] = offtin(hdbuf)
		}

		if newfwc.Count()+ctrl[x] > newsize {
			return newCorruptPatchError("newfile pos + data block exceeds expected newfile size")
		}

		// Read x bytes from diff + old into new file
		lenread, err = io.CopyBuffer(newfwc, io.LimitReader(xbyteadd, ctrl[x]), cpBuf)
		if lenread < ctrl[x] || (err != nil && err != io.EOF) {
			return newCorruptPatchBzEndError(lenread, ctrl[x], "x data block", err)
		}

		if newfwc.Count()+ctrl[y] > newsize {
			return newCorruptPatchError("newfile pos + extra block exceeds expected newfile size")
		}

		// Read y bytes from the extra block into the new file
		lenread, err = io.CopyBuffer(newfwc, io.LimitReader(epf, ctrl[y]), cpBuf)
		if lenread < ctrl[y] || (err != nil && err != io.EOF) {
			return newCorruptPatchBzEndError(lenread, ctrl[y], "y extra block", err)
		}

		// Adjust oldfile offset by z
		_, err = oldf.Seek(ctrl[z], os.SEEK_CUR)
		if err != nil {
			return err
		}
	}

	// Clean up the bzip2 reads
	if err = cpf.Close(); err != nil {
		return err
	}
	if err = dpf.Close(); err != nil {
		return err
	}
	if err = epf.Close(); err != nil {
		return err
	}
	cpfbz2 = nil
	dpfbz2 = nil
	epfbz2 = nil
	cpf = nil
	dpf = nil
	epf = nil

	return nil
}

func patchb(oldfile, patch []byte) ([]byte, error) {
	newfby := new(bytes.Buffer)
	// Use bufio here to emulate File()'s use of bufio for testing
	newfbuf := bufio.NewWriterSize(newfby, WriteBufferSize)
	oldfby := bytes.NewReader(oldfile)
	err := patchStream(oldfby, newfbuf, patch)
	newfbuf.Flush()
	fmt.Println(newfby)
	return newfby.Bytes(), err
}

// offtin reads an int64 (little endian)
func offtin(buf []byte) int64 {

	y := int(buf[7] & 0x7f)
	y = y * 256
	y += int(buf[6])
	y = y * 256
	y += int(buf[5])
	y = y * 256
	y += int(buf[4])
	y = y * 256
	y += int(buf[3])
	y = y * 256
	y += int(buf[2])
	y = y * 256
	y += int(buf[1])
	y = y * 256
	y += int(buf[0])

	if (buf[7] & 0x80) != 0 {
		y = -y
	}
	return int64(y)
}

// epfbz2.Read was not reading all the requested bytes, probably an internal buffer limitation ?
// it was encapsulated by zreadall to work around the issue
func zreadall(r io.Reader, b []byte, expected int64) (int64, error) {
	var allread int64
	var offset int64
	for {
		nreadint, err := r.Read(b[offset:])
		nread := int64(nreadint)
		if nread == expected {
			return nread, err
		}
		if err != nil {
			return allread + nread, err
		}
		allread += nread
		if allread >= expected {
			return allread, nil
		}
		offset += nread
	}
}

func min(a, b int) int {
	if a > b {
		return b
	}
	return a
}
