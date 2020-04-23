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
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"github.com/dsnet/compress/bzip2"
	"github.com/gabstv/go-bsdiff/pkg/util"
)

// Variables for streaming
var (
	writeBufferSize = 1024 * 1024
	copyBufferSize  = 1024 * 1024
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
		return fmt.Errorf("could not open oldfile '%s': %v", oldfile, err)
	}
	defer oldf.Close()

	newf, err := os.Create(newfile)
	if err != nil {
		return fmt.Errorf("could not open or create newfile '%s': %v", newfile, err)
	}

	patchbs, err := ioutil.ReadFile(patchfile)
	if err != nil {
		return fmt.Errorf("could not read patchfile '%s': %v", patchfile, err)
	}

	newfw := bufio.NewWriterSize(newf, writeBufferSize)
	err = patchStream(oldf, newfw, patchbs)

	if err != nil {
		newf.Close()
		os.Remove(newfile)
		return fmt.Errorf("bspatch: %v", err)
	}

	newfw.Flush()
	newf.Close()
	return nil
}

type ctrlTriple [3]int64

func (c *ctrlTriple) sum() int64 { return c[0] }

func (c *ctrlTriple) copy() int64 { return c[1] }

func (c *ctrlTriple) seek() int64 { return c[2] }

func patchStream(oldf io.ReadSeeker, newf io.Writer, patch []byte) error {
	//	File format:
	//		0	8	"BSDIFF40"
	//		8	8	X
	//		16	8	Y
	//		24	8	sizeof(newfile)
	//		32	X	bzip2(control block)
	//		32+X	Y	bzip2(diff block)
	//		32+X+Y	???	bzip2(extra block)
	//  The control block contains sets of triples (x,y,z) meaning:
	//  a) add x bytes from old file to x bytes from the diff block and copy
	//  b) copy y bytes from the extra block
	//  c) seek in the oldfile by z bytes
	//  Note that z can be negative.

	const HeaderLen = 32

	// Reused container vars
	var lenread int64
	var errmsg string
	header := make([]byte, HeaderLen)
	hdbuf := make([]byte, 8)
	ctrip := &ctrlTriple{}
	cpBuf := make([]byte, copyBufferSize)

	// Counter used for sanity checks
	newfwc := newWriteCounter(newf)

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
	ctrlbz := bytes.NewReader(patch)
	if _, err := ctrlbz.Seek(int64(HeaderLen), io.SeekStart); err != nil {
		return err
	}
	ctrl, err := bzip2.NewReader(ctrlbz, nil)
	if err != nil {
		return err
	}
	databz := bytes.NewReader(patch)
	if _, err := databz.Seek(int64(HeaderLen)+bzctrllen, io.SeekStart); err != nil {
		return err
	}
	data, err := bzip2.NewReader(databz, nil)
	if err != nil {
		return err
	}
	xtrabz := bytes.NewReader(patch)
	if _, err := xtrabz.Seek(int64(HeaderLen)+bzctrllen+bzdatalen, io.SeekStart); err != nil {
		return err
	}
	xtra, err := bzip2.NewReader(xtrabz, nil)
	if err != nil {
		return err
	}

	xbyteadd := newByteAddReader(data, oldf)

	for newfwc.Count() < newsize {
		// Read control data
		for i := 0; i < 2; i++ {
			lenread, err := io.ReadFull(ctrl, hdbuf)
			if lenread != 8 || (err != nil && err != io.EOF) {
				// format "corrupt patch or bz stream ended: control data read (lenread/8) err.Error()"
				return newCorruptPatchBzEndError(int64(lenread), 8, "control data", err)
			}
			ctrip[i] = offtin(hdbuf)
		}

		if newfwc.Count()+ctrip.sum() > newsize {
			return newCorruptPatchError("newfile pos + data block exceeds expected newfile size")
		}

		// Read x bytes from diff + old into new file
		lenread, err = io.CopyBuffer(newfwc, io.LimitReader(xbyteadd, ctrip.sum()), cpBuf)
		if lenread < ctrip.sum() || (err != nil && err != io.EOF) {
			return newCorruptPatchBzEndError(lenread, ctrip.sum(), "x data block", err)
		}

		if newfwc.Count()+ctrip.copy() > newsize {
			return newCorruptPatchError("newfile pos + extra block exceeds expected newfile size")
		}

		// Read bytes from the extra block into the new file
		lenread, err = io.CopyBuffer(newfwc, io.LimitReader(xtra, ctrip.copy()), cpBuf)
		if lenread < ctrip.copy() || (err != nil && err != io.EOF) {
			return newCorruptPatchBzEndError(lenread, ctrip.copy(), "y extra block", err)
		}

		// Adjust oldfile offset by ctrl triple
		_, err = oldf.Seek(ctrip.seek(), os.SEEK_CUR)
		if err != nil {
			return err
		}
	}

	// Clean up the bzip2 reads
	if err = ctrl.Close(); err != nil {
		return err
	}
	if err = data.Close(); err != nil {
		return err
	}
	if err = xtra.Close(); err != nil {
		return err
	}
	ctrlbz = nil
	databz = nil
	xtrabz = nil
	ctrl = nil
	data = nil
	xtra = nil

	return nil
}

func patchb(oldfile, patch []byte) ([]byte, error) {
	newfby := new(bytes.Buffer)
	// Use bufio here to emulate File()'s use of bufio for testing
	newfbuf := bufio.NewWriterSize(newfby, writeBufferSize)
	oldfby := bytes.NewReader(oldfile)
	err := patchStream(oldfby, newfbuf, patch)
	newfbuf.Flush()
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

func min(a, b int) int {
	if a > b {
		return b
	}
	return a
}
