package main

import (
	"cmp"
	"context"
	"fmt"
	mathrand "math/rand/v2"
	"regexp"
	"slices"
	"sync"
	"time"
)

const (
	operationWrite = "write"
	operationRead  = "read"
)

const (
	// percentile values reported for latency.
	percentile50 = 50
	percentile90 = 90
	percentile99 = 99
	percentMax   = 100
	bytesPerMiB  = 1 << 20
)

const (
	reportHeaderFormat = "%8s %-6s %10s %8s %10s %10s %10s %10s %10s %10s\n"
	reportRowFormat    = "%8d %-6s %10d %8d %10.1f %10.2f %10s %10s %10s %10s\n"
	// reportErrorFormat is filled with the operation and then used as the format of errorCounter.print.
	reportErrorFormat = "         %s error (%%d): %%s\n"
	// pathPlaceholder and numberPlaceholder replace paths and numbers in error messages,
	// so that the same error on different files is counted once.
	pathPlaceholder   = "<path>"
	numberPlaceholder = "N"
)

var (
	// paths matches the absolute paths replaced by pathPlaceholder: a slash at the start or after a space or quote.
	paths = regexp.MustCompile(`(^|[\s"'])/[^\s:"']+`)
	// numbers matches the numbers replaced by numberPlaceholder.
	numbers = regexp.MustCompile(`[0-9]+`)
)

// errorCounter counts errors by message, with numbers replaced.
type errorCounter struct {
	counts map[string]int
	total  int
}

func newErrorCounter() *errorCounter {
	return &errorCounter{counts: map[string]int{}}
}

func (c *errorCounter) add(err error) {
	message := paths.ReplaceAllString(err.Error(), "${1}"+pathPlaceholder)
	c.counts[numbers.ReplaceAllString(message, numberPlaceholder)]++
	c.total++
}

func (c *errorCounter) merge(other *errorCounter) {
	for message, count := range other.counts {
		c.counts[message] += count
	}
	c.total += other.total
}

// print prints each message with its count, most frequent first. format takes the count and the message.
func (c *errorCounter) print(format string) {
	messages := make([]string, 0, len(c.counts))
	for message := range c.counts {
		messages = append(messages, message)
	}
	slices.SortFunc(messages, func(a, b string) int { return cmp.Compare(c.counts[b], c.counts[a]) })
	for _, message := range messages {
		fmt.Printf(format, c.counts[message], message)
	}
}

// result is what workers measured for one operation.
type result struct {
	latencies []time.Duration
	bytes     int64
	errors    *errorCounter
}

func newResult() *result {
	return &result{errors: newErrorCounter()}
}

func (r *result) merge(other *result) {
	r.latencies = append(r.latencies, other.latencies...)
	r.bytes += other.bytes
	r.errors.merge(other.errors)
}

func measure(ctx context.Context, target backend, writers int, config *settings) (*result, *result, time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, config.duration)
	defer cancel()
	writes, reads := newResult(), newResult()
	var mutex sync.Mutex
	var group sync.WaitGroup
	start := time.Now()
	run := func(total *result, operation func() (int, error)) {
		group.Go(func() {
			measured := work(ctx, operation)
			mutex.Lock()
			total.merge(measured)
			mutex.Unlock()
		})
	}
	for range writers {
		run(writes, func() (int, error) {
			return config.size, target.write(ctx, fileName(mathrand.IntN(config.files)), newFileData(config.size))
		})
	}
	for range config.readers {
		run(reads, func() (int, error) {
			content, err := target.read(ctx, fileName(mathrand.IntN(config.files)))
			if err == nil {
				err = checkFileData(content)
			}
			return len(content), err
		})
	}
	group.Wait()
	return writes, reads, time.Since(start)
}

// work repeats operation until the stage ends. A call cut off by the end of the stage is not counted.
func work(ctx context.Context, operation func() (int, error)) *result {
	measured := newResult()
	for !finished(ctx) {
		start := time.Now()
		size, err := operation()
		if finished(ctx) {
			break
		}
		if err != nil {
			measured.errors.add(err)
			continue
		}
		measured.latencies = append(measured.latencies, time.Since(start))
		measured.bytes += int64(size)
	}
	return measured
}

// finished reports whether the stage is over. gRPC can return DeadlineExceeded before ctx.Err() is set,
// so the deadline is also compared with the clock.
func finished(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}

func printHeader() {
	fmt.Printf(reportHeaderFormat, "writers", "", "ops", "errors", "ops/s", "MiB/s", "p50", "p90", "p99", "max")
}

func report(writers int, operation string, measured *result, elapsed time.Duration) {
	slices.Sort(measured.latencies)
	seconds := elapsed.Seconds()
	fmt.Printf(reportRowFormat, writers, operation, len(measured.latencies), measured.errors.total,
		float64(len(measured.latencies))/seconds, float64(measured.bytes)/bytesPerMiB/seconds,
		percentile(measured.latencies, percentile50), percentile(measured.latencies, percentile90),
		percentile(measured.latencies, percentile99), percentile(measured.latencies, percentMax))
	measured.errors.print(fmt.Sprintf(reportErrorFormat, operation))
}

// percentile returns the latency at the given percent of the sorted latencies, rounded to microseconds.
func percentile(sorted []time.Duration, percent int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	index := (len(sorted) - 1) * percent / percentMax
	return sorted[index].Round(time.Microsecond)
}
