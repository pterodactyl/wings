// SPDX-License-Identifier: BSD-3-Clause

// Code in this file was copied from `go/src/os/file_posix.go`.

// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the `go.LICENSE` file.

//go:build unix || (js && wasm) || wasip1 || windows

package ufs

import (
	"golang.org/x/sys/unix"
)

// ignoringEINTR makes a function call and repeats it if it returns an
// EINTR error. This appears to be required even though we install all
// signal handlers with SA_RESTART: see https://go.dev/issue/22838,
// https://go.dev/issue/38033, https://go.dev/issue/38836,
// https://go.dev/issue/40846. Also, https://go.dev/issue/20400 and
// https://go.dev/issue/36644 are issues in which a signal handler is
// installed without setting SA_RESTART. None of these are the common case,
// but there are enough of them that it seems that we can't avoid
// an EINTR loop.
func ignoringEINTR(fn func() error) error {
	for {
		err := fn()
		if err != unix.EINTR {
			return err
		}
	}
}

// syscallMode returns the syscall-specific mode bits from Go's portable mode bits.
func syscallMode(i FileMode) (o FileMode) {
	o |= i.Perm()
	if i&ModeSetuid != 0 {
		o |= unix.S_ISUID
	}
	if i&ModeSetgid != 0 {
		o |= unix.S_ISGID
	}
	if i&ModeSticky != 0 {
		o |= unix.S_ISVTX
	}
	// No mapping for Go's ModeTemporary (plan9 only).
	return
}
