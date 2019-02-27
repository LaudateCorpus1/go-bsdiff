package bspatch

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestPatch(t *testing.T) {
	oldfile := []byte{
		0x66, 0xFF, 0xD1, 0x55, 0x56, 0x10, 0x30, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xD1,
	}
	newfilecomp := []byte{
		0x66, 0xFF, 0xD1, 0x55, 0x56, 0x10, 0x30, 0x00,
		0x44, 0x45, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xD1, 0xFF, 0xD1,
	}
	patchfile := []byte{
		0x42, 0x53, 0x44, 0x49, 0x46, 0x46, 0x34, 0x30,
		0x29, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x2A, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x13, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x42, 0x5A, 0x68, 0x39, 0x31, 0x41, 0x59, 0x26,
		0x53, 0x59, 0xDA, 0xE4, 0x46, 0xF2, 0x00, 0x00,
		0x05, 0xC0, 0x00, 0x4A, 0x09, 0x20, 0x00, 0x22,
		0x34, 0xD9, 0x06, 0x06, 0x4B, 0x21, 0xEE, 0x17,
		0x72, 0x45, 0x38, 0x50, 0x90, 0xDA, 0xE4, 0x46,
		0xF2, 0x42, 0x5A, 0x68, 0x39, 0x31, 0x41, 0x59,
		0x26, 0x53, 0x59, 0x30, 0x88, 0x1C, 0x89, 0x00,
		0x00, 0x02, 0xC4, 0x00, 0x44, 0x00, 0x06, 0x00,
		0x20, 0x00, 0x21, 0x21, 0xA0, 0xC3, 0x1B, 0x03,
		0x3C, 0x5D, 0xC9, 0x14, 0xE1, 0x42, 0x40, 0xC2,
		0x20, 0x72, 0x24, 0x42, 0x5A, 0x68, 0x39, 0x31,
		0x41, 0x59, 0x26, 0x53, 0x59, 0x65, 0x25, 0x30,
		0x43, 0x00, 0x00, 0x00, 0x40, 0x02, 0xC0, 0x00,
		0x20, 0x00, 0x00, 0x00, 0xA0, 0x00, 0x22, 0x1F,
		0xA4, 0x19, 0x82, 0x58, 0x5D, 0xC9, 0x14, 0xE1,
		0x42, 0x41, 0x94, 0x94, 0xC1, 0x0C,
	}
	newfile, err := Bytes(oldfile, patchfile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(newfile, newfilecomp) {
		t.Fatal("expected:", newfilecomp, "got:", newfile)
	}
}

func TestOfftin(t *testing.T) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, 9001)
	n := offtin(buf)
	if n != 9001 {
		t.Fatal(n, "!=", 9001)
	}
}
