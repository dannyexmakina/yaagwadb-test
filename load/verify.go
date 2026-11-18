package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"yaagwadb-test/daemonclient"
)

const (
	verifyHeader              = "\nverification"
	verifyDaemonFormat        = "  daemon:     %s\n"
	verifyQueueFormat         = "  queue %s: pending %d, running %d, failed %d\n"
	verifyEntriesFormat       = "  entries:    %d (%d unexpected, %d of %d files missing)\n"
	verifyUnexpected          = "    unexpected entry: %s\n"
	verifyFilesFormat         = "  files:      %d of %d read and match their checksum\n"
	verifyFileError           = "    %s: %v\n"
	verifyWriteFormat         = "  new write:  %s\n"
	verifyResultFormat        = "  result:     %s\n"
	verifyOK                  = "ok"
	verifyPassed              = "passed"
	verifyFailed              = "FAILED"
	verifyNotRespondingFormat = "not responding (%v)"
)

// verify checks after the run that the daemon still responds, that the directory holds only the files the test wrote,
// that every file reads back intact, and that a new write and read still work.
func verify(ctx context.Context, client *daemonclient.Client, target backend, config *settings) bool {
	fmt.Println(verifyHeader)
	passed := true
	fail := func() { passed = false }

	status, err := client.Status(ctx)
	if err != nil {
		fmt.Printf(verifyDaemonFormat, fmt.Sprintf(verifyNotRespondingFormat, err))
		fmt.Printf(verifyResultFormat, verifyFailed)
		return false
	}
	fmt.Printf(verifyDaemonFormat, verifyOK)
	for _, queue := range status.Queues {
		fmt.Printf(verifyQueueFormat, queue.Queue, queue.Pending, queue.Running, queue.Failed)
	}

	names, err := target.names(ctx)
	if err != nil {
		fmt.Printf(verifyFileError, "readdir", err)
		fail()
	}
	var unexpected []string
	for _, name := range names {
		if !strings.HasPrefix(name, fileNamePrefix) && !strings.HasPrefix(name, holdNamePrefix) {
			unexpected = append(unexpected, name)
		}
	}
	missing := 0
	for index := range config.files {
		if !slices.Contains(names, fileName(index)) {
			missing++
		}
	}
	fmt.Printf(verifyEntriesFormat, len(names), len(unexpected), missing, config.files)
	for _, name := range unexpected {
		fmt.Printf(verifyUnexpected, name)
	}
	if len(unexpected) > 0 || missing > 0 {
		fail()
	}

	intact := 0
	for index := range config.files {
		content, err := target.read(ctx, fileName(index))
		if err == nil {
			err = checkFileData(content)
		}
		if err != nil {
			fmt.Printf(verifyFileError, fileName(index), err)
			fail()
			continue
		}
		intact++
	}
	fmt.Printf(verifyFilesFormat, intact, config.files)

	if err := writeAndReadBack(ctx, target, config.size); err != nil {
		fmt.Printf(verifyWriteFormat, err)
		fail()
	} else {
		fmt.Printf(verifyWriteFormat, verifyOK)
	}

	if passed {
		fmt.Printf(verifyResultFormat, verifyPassed)
	} else {
		fmt.Printf(verifyResultFormat, verifyFailed)
	}
	return passed
}

func writeAndReadBack(ctx context.Context, target backend, size int) error {
	if err := target.write(ctx, verifyFileName, newFileData(size)); err != nil {
		return err
	}
	content, err := target.read(ctx, verifyFileName)
	if err == nil {
		err = checkFileData(content)
	}
	if err != nil {
		return err
	}
	return target.remove(ctx, verifyFileName)
}
