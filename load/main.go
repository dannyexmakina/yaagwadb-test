// load writes and reads files concurrently and reports throughput, latency, and errors.
// It drives the daemon either through gRPC or through the FUSE mount, can push past the daemon's limits
// (ramping concurrency, holding writes open), and checks afterwards that the daemon still works and the data is intact.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"yaagwadb-test/daemonclient"

	"github.com/BurntSushi/toml"
)

const (
	defaultConfigPath = "/etc/yaagwadb/config.toml"
	defaultSocket     = "/run/yaagwadb/daemon.sock"
	defaultMode       = modeGRPC
	defaultWriters    = 4
	defaultReaders    = 16
	defaultFiles      = 100
	defaultSize       = 64 << 10
	defaultDuration   = 30 * time.Second
	// rampDisabled as -ramp-max runs a single stage.
	rampDisabled = 0
	// rampFactor multiplies the writers at every ramp stage.
	rampFactor = 2
	// holdSettle is how long the held writes are left before they are probed.
	holdSettle = time.Second
	// noHold as -hold holds no writes open.
	noHold = 0
	// directoryNamePrefix is prefixed to the directory the files are written into. A timestamp follows it.
	directoryNamePrefix = "load-"
	// directoryTimeFormat is the format of the timestamp in the directory name.
	directoryTimeFormat = "20060102-150405"
	exitFailure         = 1
)

const (
	modeGRPC  = "grpc"
	modeMount = "mount"
)

const (
	usageErrorSizeTooSmall = "-size must be larger than %d (the checksum length)"
	usageErrorPositive     = "-writers + -readers, -files and -duration must be positive, and -hold not negative"
	usageErrorRamp         = "-ramp-max must be 0, or at least -writers with -writers positive"
	usageErrorMode         = "-mode must be %s or %s"
	usageErrorMountpoint   = "-mountpoint is required with -mode %s"
	settingsFormat         = "mode %s, socket %s, directory %q, %d files of %d bytes, %d readers, %s per stage\n"
	setupErrorFormat       = "setup: %w"
	holdFormat             = "\nholding %d writes open: %d held, %d failed\n"
	holdErrorFormat        = "  hold error (%d): %s\n"
	releaseFormat          = "\nreleased %d held writes: %d failed\n"
	stopRampMessage        = "\nerrors appeared; the ramp stops here"
	verificationFailed     = "verification failed"
)

type settings struct {
	mode       string
	socket     string
	mountpoint string
	writers    int
	rampMax    int
	readers    int
	files      int
	size       int
	duration   time.Duration
	hold       int
	keep       bool
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
	flag.StringVar(&config.mode, "mode", defaultMode, "how to reach the daemon: grpc or mount")
	flag.StringVar(&config.socket, "socket", "", "daemon socket (default: socket in the configuration file). Used in both modes for setup and checks")
	flag.StringVar(&config.mountpoint, "mountpoint", "", "mount point to write and read through (-mode mount)")
	flag.IntVar(&config.writers, "writers", defaultWriters, "number of concurrent writers (first stage when ramping)")
	flag.IntVar(&config.rampMax, "ramp-max", rampDisabled, "double the writers every stage up to this number; stop at the first stage with errors")
	flag.IntVar(&config.readers, "readers", defaultReaders, "number of concurrent readers")
	flag.IntVar(&config.files, "files", defaultFiles, "number of files written and read")
	flag.IntVar(&config.size, "size", defaultSize, "size of each file in bytes")
	flag.DurationVar(&config.duration, "duration", defaultDuration, "how long each stage runs")
	flag.IntVar(&config.hold, "hold", noHold, "number of writes held open (started but not finished) during the run")
	flag.BoolVar(&config.keep, "keep", false, "keep the written files")
	flag.Parse()
	switch {
	case config.mode != modeGRPC && config.mode != modeMount:
		return nil, fmt.Errorf(usageErrorMode, modeGRPC, modeMount)
	case config.mode == modeMount && config.mountpoint == "":
		return nil, fmt.Errorf(usageErrorMountpoint, modeMount)
	case config.size <= checksumLength:
		return nil, fmt.Errorf(usageErrorSizeTooSmall, checksumLength)
	case config.writers+config.readers <= 0 || config.writers < 0 || config.readers < 0 || config.files <= 0 ||
		config.duration <= 0 || config.hold < 0:
		return nil, errors.New(usageErrorPositive)
	case config.rampMax != rampDisabled && (config.writers <= 0 || config.rampMax < config.writers):
		return nil, errors.New(usageErrorRamp)
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
	client, err := daemonclient.Dial(config.socket)
	if err != nil {
		return err
	}
	defer client.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	directoryName := directoryNamePrefix + time.Now().Format(directoryTimeFormat)
	target, err := newBackend(ctx, config, client, directoryName)
	if err != nil {
		return fmt.Errorf(setupErrorFormat, err)
	}
	fmt.Printf(settingsFormat, config.mode, config.socket, directoryName, config.files, config.size, config.readers, config.duration)
	// Every file exists before the readers start
	for index := range config.files {
		if err := target.write(ctx, fileName(index), newFileData(config.size)); err != nil {
			return fmt.Errorf(setupErrorFormat, err)
		}
	}

	release := holdWrites(ctx, target, config.hold)
	runStages(ctx, target, config)
	release()

	// Checks run even after Ctrl-C
	checkContext := context.Background()
	if !verify(checkContext, client, target, config) {
		return errors.New(verificationFailed)
	}
	if config.keep {
		return nil
	}
	return cleanup(checkContext, target, config)
}

// runStages runs one stage, or doubles the writers every stage up to -ramp-max and stops at the first stage with errors.
func runStages(ctx context.Context, target backend, config *settings) {
	printHeader()
	for writers := config.writers; ; writers *= rampFactor {
		writes, reads, elapsed := measure(ctx, target, writers, config)
		report(writers, operationWrite, writes, elapsed)
		report(writers, operationRead, reads, elapsed)
		if config.rampMax == rampDisabled || ctx.Err() != nil {
			return
		}
		if writes.errors.total > 0 || reads.errors.total > 0 {
			fmt.Println(stopRampMessage)
			return
		}
		if writers*rampFactor > config.rampMax {
			return
		}
	}
}

// holdWrites starts writes that are not finished until the returned function is called.
// After holdSettle, every held write is probed, because the daemon reports a failure only after the stream started.
func holdWrites(ctx context.Context, target backend, count int) func() {
	if count == noHold {
		return func() {}
	}
	var held []*heldWrite
	failures := newErrorCounter()
	for index := range count {
		write, err := target.hold(ctx, holdName(index))
		if err != nil {
			failures.add(err)
			continue
		}
		held = append(held, write)
	}
	time.Sleep(holdSettle)
	alive := 0
	for _, write := range held {
		if err := write.probe(); err != nil {
			failures.add(err)
			continue
		}
		alive++
	}
	fmt.Printf(holdFormat, count, alive, failures.total)
	failures.print(holdErrorFormat)
	fmt.Println()
	return func() {
		failures := newErrorCounter()
		for _, write := range held {
			if err := write.release(); err != nil {
				failures.add(err)
			}
		}
		fmt.Printf(releaseFormat, len(held), failures.total)
		failures.print(holdErrorFormat)
	}
}

func cleanup(ctx context.Context, target backend, config *settings) error {
	for index := range config.files {
		if err := target.remove(ctx, fileName(index)); err != nil {
			return err
		}
	}
	// Held writes may or may not have left a file depending on the mode
	for index := range config.hold {
		_ = target.remove(ctx, holdName(index))
	}
	return target.removeDirectory(ctx)
}
