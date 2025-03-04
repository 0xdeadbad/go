// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package check implements the FIPS-140 load-time code+data verification.
// Every FIPS package providing cryptographic functionality except hmac and sha256
// must import crypto/internal/fips/check, so that the verification happens
// before initialization of package global variables.
// The hmac and sha256 packages are used by this package, so they cannot import it.
// Instead, those packages must be careful not to change global variables during init.
// (If necessary, we could have check call a PostCheck function in those packages
// after the check has completed.)
package check

import (
	"crypto/internal/fips/hmac"
	"crypto/internal/fips/sha256"
	"internal/byteorder"
	"internal/godebug"
	"io"
	"runtime"
	"unsafe"
)

// Enabled reports whether verification was enabled.
// If Enabled returns true, then verification succeeded,
// because if it failed the binary would have panicked at init time.
func Enabled() bool {
	return enabled
}

var enabled bool  // set when verification is enabled
var verified bool // set when verification succeeds, for testing

// supported reports whether the current GOOS/GOARCH is supported at all.
func supported() bool {
	// See cmd/internal/obj/fips.go's EnableFIPS for commentary.
	switch {
	case runtime.GOARCH == "wasm",
		runtime.GOOS == "windows" && runtime.GOARCH == "386",
		runtime.GOOS == "windows" && runtime.GOARCH == "arm",
		runtime.GOOS == "windows" && runtime.GOARCH == "arm64":
		return false
	}
	return true
}

// linkinfo holds the go:fipsinfo symbol prepared by the linker.
// See cmd/link/internal/ld/fips.go for details.
//
//go:linkname linkinfo go:fipsinfo
var linkinfo struct {
	Magic [16]byte
	Sum   [32]byte
	Self  uintptr
	Sects [4]struct {
		// Note: These must be unsafe.Pointer, not uintptr,
		// or else checkptr panics about turning uintptrs
		// into pointers into the data segment during
		// go test -race.
		Start unsafe.Pointer
		End   unsafe.Pointer
	}
}

// "\xff"+fipsMagic is the expected linkinfo.Magic.
// We avoid writing that explicitly so that the string does not appear
// elsewhere in normal binaries, just as a precaution.
const fipsMagic = " Go fipsinfo \xff\x00"

var zeroSum [32]byte

func init() {
	v := godebug.New("#fips140").Value()
	enabled = v != "" && v != "off"
	if !enabled {
		return
	}

	switch v {
	case "on", "only", "debug":
		// ok
	default:
		panic("fips140: unknown GODEBUG setting fips140=" + v)
	}

	if !supported() {
		panic("fips140: unavailable on " + runtime.GOOS + "-" + runtime.GOARCH)
	}

	if linkinfo.Magic[0] != 0xff || string(linkinfo.Magic[1:]) != fipsMagic || linkinfo.Sum == zeroSum {
		panic("fips140: no verification checksum found")
	}

	h := hmac.New(sha256.New, make([]byte, 32))
	w := io.Writer(h)

	/*
		// Uncomment for debugging.
		// Commented (as opposed to a const bool flag)
		// to avoid import "os" in default builds.
		f, err := os.Create("fipscheck.o")
		if err != nil {
			panic(err)
		}
		w = io.MultiWriter(h, f)
	*/

	w.Write([]byte("go fips object v1\n"))

	var nbuf [8]byte
	for _, sect := range linkinfo.Sects {
		n := uintptr(sect.End) - uintptr(sect.Start)
		byteorder.BePutUint64(nbuf[:], uint64(n))
		w.Write(nbuf[:])
		w.Write(unsafe.Slice((*byte)(sect.Start), n))
	}
	sum := h.Sum(nil)

	if [32]byte(sum) != linkinfo.Sum {
		panic("fips140: verification mismatch")
	}

	if v == "debug" {
		println("fips140: verified code+data")
	}

	verified = true
}
