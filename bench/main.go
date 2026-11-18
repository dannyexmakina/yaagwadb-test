// bench measures the overhead of reading and writing through YaagwaDB against the plain file system.
// One file is written and read at a time, so the latency of each operation is compared, not throughput under load.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"yaagwadb/protocol"
	"yaagwadb-test/daemonclient"

	"github.com/BurntSushi/toml"
)

const (
	defaultConfigPath = "/etc/yaagwadb/config.toml"
	defaultSocket     = "/run/yaagwadb/daemon.sock"
	defaultSizes      = "4096,65536,1048576,16777216"
	defaultCount      = 30
	// directoryNamePrefix is prefixed to the directories the files are written into. A timestamp follows it.
	directoryNamePrefix = "bench-"
	directoryTimeFormat = "20060102-150405"
	directoryMode       = 0o755
	fileMode            = 0o644
	noOwnerID           = 0
	sizeSeparator       = ","
	exitFailure         = 1
)

const (
	// percentile values reported for latency.
	percentile50 = 50
	percentile99 = 99
	percentMax   = 100
	bytesPerMiB  = 1 << 20
	// noBaseline is shown when there is no plain file system result to compare with.
	noBaseline = "-"
)

const (
	operationWrite = "write"
	operationRead  = "read"
)

// Targets in the order they are measured. targetPlain is the baseline the others are compared with.
const (
	targetPlain       = "plain"
	targetPlainNoSync = "plain-nosync"
	targetGRPC        = "grpc"
	targetMount       = "mount"
)

const (
	usageErrorCount   = "-count must be positive"
	usageErrorSize    = "invalid size %q in -sizes"
	settingsFormat    = "socket %s, plain directory %s, mount %s, %d operations per row\n"
	mountNotUsed      = "(not measured)"
	headerFormat      = "%10s %-6s %-13s %10s %10s %10s %10s %9s\n"
	rowFormat         = "%10d %-6s %-13s %10s %10s %10s %10.2f %9s\n"
	ratioFormat       = "x%.2f"
	resultErrorFormat = "%s %s %d bytes: %w"
	checkErrorFormat  = "read back %d bytes, want %d"
	notesText         = `
notes
  plain        os.WriteFile then fsync of the file, os.ReadFile (the daemon also fsyncs each write)
  plain-nosync os.WriteFile without fsync
  grpc         Write, Lookup then Read of the file size
  mount        os.WriteFile and os.ReadFile through the mount
  vs plain     median latency divided by the median of plain
  Reads of plain may be served from the page cache.`
)

type settings struct {
	socket     string
	mountpoint string
	plain      string
	sizes      []int
	count      int
	keep       bool
}

// target writes and reads one named file.
type target struct {
	name  string
	write func(ctx context.Context, name string, content []byte) error
	read  func(ctx context.Context, name string) ([]byte, error)
}

func main() {
	config, err := parseSettings()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitFailure)
	}
	if err := run(config); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitFailure)
	}
}

func parseSettings() (*settings, error) {
	config := &settings{}
	configPath := flag.String("c", defaultConfigPath, "configuration file to read the socket from (ignored when -socket is given)")
	flag.StringVar(&config.socket, "socket", "", "daemon socket (default: socket in the configuration file)")
	flag.StringVar(&config.mountpoint, "mountpoint", "", "mount point to measure through. Empty skips the mount")
	flag.StringVar(&config.plain, "plain", os.TempDir(), "directory on the plain file system to compare with")
	sizes := flag.String("sizes", defaultSizes, "file sizes in bytes, separated by commas")
	flag.IntVar(&config.count, "count", defaultCount, "operations per size, target and operation")
	flag.BoolVar(&config.keep, "keep", false, "keep the written files")
	flag.Parse()
	if config.count <= 0 {
		return nil, errors.New(usageErrorCount)
	}
	for _, text := range strings.Split(*sizes, sizeSeparator) {
		size, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || size <= 0 {
			return nil, fmt.Errorf(usageErrorSize, text)
		}
		config.sizes = append(config.sizes, size)
	}
	if config.socket == "" {
		socket, err := socketFromConfig(*configPath)
		if err != nil {
			return nil, err
		}
		config.socket = socket
	}
	return config, nil
}

func socketFromConfig(path string) (string, error) {
	file := struct {
		Socket string `toml:"socket"`
	}{Socket: defaultSocket}
	if _, err := toml.DecodeFile(path, &file); err != nil {
		return "", err
	}
	return file.Socket, nil
}

func run(config *settings) error {
	ctx := context.Background()
	client, err := daemonclient.Dial(config.socket)
	if err != nil {
		return err
	}
	defer client.Close()
	directoryName := directoryNamePrefix + time.Now().Format(directoryTimeFormat)

	targets, cleanups, err := prepareTargets(ctx, config, client, directoryName)
	defer func() {
		if config.keep {
			return
		}
		for _, cleanup := range slices.Backward(cleanups) {
			cleanup()
		}
	}()
	if err != nil {
		return err
	}
	mount := config.mountpoint
	if mount == "" {
		mount = mountNotUsed
	}
	fmt.Printf(settingsFormat, config.socket, config.plain, mount, config.count)
	fmt.Printf(headerFormat, "size", "op", "target", "p50", "p99", "max", "MiB/s", "vs plain")
	for _, size := range config.sizes {
		content := make([]byte, size)
		rand.Read(content)
		for _, operation := range []string{operationWrite, operationRead} {
			var baseline time.Duration
			for _, current := range targets {
				latencies, err := measure(ctx, current, operation, size, content, config.count)
				if err != nil {
					return fmt.Errorf(resultErrorFormat, current.name, operation, size, err)
				}
				median := percentile(latencies, percentile50)
				comparison := noBaseline
				if current.name == targetPlain {
					baseline = median
				} else if baseline > 0 {
					comparison = fmt.Sprintf(ratioFormat, float64(median)/float64(baseline))
				}
				fmt.Printf(rowFormat, size, operation, current.name, median, percentile(latencies, percentile99),
					percentile(latencies, percentMax), float64(size)/bytesPerMiB/median.Seconds(), comparison)
			}
		}
	}
	fmt.Println(notesText)
	return nil
}

// prepareTargets creates a directory for each target and returns the targets with the functions that remove what they wrote.
func prepareTargets(ctx context.Context, config *settings, client *daemonclient.Client, directoryName string) ([]target, []func(), error) {
	var cleanups []func()
	plainDirectory := filepath.Join(config.plain, directoryName)
	if err := os.Mkdir(plainDirectory, directoryMode); err != nil {
		return nil, cleanups, err
	}
	cleanups = append(cleanups, func() { os.RemoveAll(plainDirectory) })
	targets := []target{
		plainTarget(targetPlain, plainDirectory, true),
		plainTarget(targetPlainNoSync, plainDirectory, false),
	}

	directory, err := client.Mkdir(ctx, daemonclient.RootInode, directoryName, directoryMode, noOwnerID, noOwnerID)
	if err != nil {
		return nil, cleanups, err
	}
	written := map[string]bool{}
	cleanups = append(cleanups, func() {
		for name := range written {
			_ = client.Unlink(ctx, directory.Inode, name)
		}
		_ = client.Rmdir(ctx, daemonclient.RootInode, directoryName)
	})
	targets = append(targets, target{
		name: targetGRPC,
		write: func(ctx context.Context, name string, content []byte) error {
			written[name] = true
			return client.Write(ctx, &protocol.WriteTarget{ParentInode: directory.Inode, Name: name, Mode: fileMode,
				Uid: noOwnerID, Gid: noOwnerID}, content)
		},
		read: func(ctx context.Context, name string) ([]byte, error) {
			inode, err := client.Lookup(ctx, directory.Inode, name)
			if err != nil {
				return nil, err
			}
			// The size is given, as the mount does, so the daemon sizes its buffer to the file
			return client.Read(ctx, inode.Inode, int64(inode.Size))
		},
	})

	if config.mountpoint != "" {
		mountDirectory := filepath.Join(config.mountpoint, directoryName+targetMount)
		if err := os.Mkdir(mountDirectory, directoryMode); err != nil {
			return nil, cleanups, err
		}
		cleanups = append(cleanups, func() { os.RemoveAll(mountDirectory) })
		targets = append(targets, plainTarget(targetMount, mountDirectory, false))
	}
	return targets, cleanups, nil
}

// plainTarget writes and reads files in directory with the os package. sync adds fsync after each write.
func plainTarget(name, directory string, sync bool) target {
	return target{
		name: name,
		write: func(_ context.Context, file string, content []byte) error {
			if !sync {
				return os.WriteFile(filepath.Join(directory, file), content, fileMode)
			}
			handle, err := os.OpenFile(filepath.Join(directory, file), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
			if err != nil {
				return err
			}
			if _, err := handle.Write(content); err != nil {
				handle.Close()
				return err
			}
			if err := handle.Sync(); err != nil {
				handle.Close()
				return err
			}
			return handle.Close()
		},
		read: func(_ context.Context, file string) ([]byte, error) {
			return os.ReadFile(filepath.Join(directory, file))
		},
	}
}

// measure writes count new files, or reads one file count times, and returns the sorted latencies.
// Each target uses its own file names, so the plain targets do not read each other's files.
func measure(ctx context.Context, current target, operation string, size int, content []byte, count int) ([]time.Duration, error) {
	fileName := func(index int) string {
		return fmt.Sprintf("%s-%d-%d", current.name, size, index)
	}
	latencies := make([]time.Duration, 0, count)
	if operation == operationWrite {
		for index := range count {
			start := time.Now()
			if err := current.write(ctx, fileName(index), content); err != nil {
				return nil, err
			}
			latencies = append(latencies, time.Since(start))
		}
	} else {
		for index := range count {
			start := time.Now()
			data, err := current.read(ctx, fileName(index))
			if err != nil {
				return nil, err
			}
			latencies = append(latencies, time.Since(start))
			if len(data) != size {
				return nil, fmt.Errorf(checkErrorFormat, len(data), size)
			}
		}
	}
	slices.Sort(latencies)
	return latencies, nil
}

// percentile returns the latency at the given percent of the sorted latencies, rounded to microseconds.
func percentile(sorted []time.Duration, percent int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := (len(sorted) - 1) * percent / percentMax
	return sorted[index].Round(time.Microsecond)
}
