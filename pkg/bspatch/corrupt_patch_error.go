package bspatch

import "fmt"

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
