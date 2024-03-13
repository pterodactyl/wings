// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright (c) 2024 Matthew Penner

// Package ufs provides an abstraction layer for performing I/O on filesystems.
// This package is designed to be used in-place of standard `os` package I/O
// calls, and is not designed to be used as a generic filesystem abstraction
// like the `io/fs` package.
//
// The primary use-case of this package was to provide a "chroot-like" `os`
// wrapper, so we can safely sandbox I/O operations within a directory and
// use untrusted arbitrary paths.
package ufs
