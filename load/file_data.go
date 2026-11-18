package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"strconv"
)

const (
	// checksumLength is the length of the SHA-256 at the start of each content. The rest of the content is random bytes.
	checksumLength = sha256.Size
	// fileNamePrefix and holdNamePrefix are prefixed to the file number.
	fileNamePrefix = "file-"
	holdNamePrefix = "hold-"
	// verifyFileName is the file written and read back after the run.
	verifyFileName = "verify"
)

const errorChecksumMismatch = "checksum mismatch (%d bytes)"

// newFileData returns the SHA-256 of random bytes followed by those bytes.
func newFileData(size int) []byte {
	content := make([]byte, size)
	rand.Read(content[checksumLength:])
	checksum := sha256.Sum256(content[checksumLength:])
	copy(content, checksum[:])
	return content
}

// checkFileData reports whether the content matches its own checksum. A content mixed from two writes fails the check.
func checkFileData(content []byte) error {
	if len(content) <= checksumLength {
		return fmt.Errorf(errorChecksumMismatch, len(content))
	}
	if checksum := sha256.Sum256(content[checksumLength:]); !bytes.Equal(checksum[:], content[:checksumLength]) {
		return fmt.Errorf(errorChecksumMismatch, len(content))
	}
	return nil
}

func fileName(index int) string {
	return fileNamePrefix + strconv.Itoa(index)
}

func holdName(index int) string {
	return holdNamePrefix + strconv.Itoa(index)
}
