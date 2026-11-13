package docker

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/moby/moby/api/pkg/stdcopy"
)

type (
	// LogOptions describes the log stream StreamContainerLogs reads.
	LogOptions struct {
		// Follow streams logs until the reader ends instead of reading a snapshot.
		Follow bool
		// TTY marks a raw TTY stream; otherwise the stream is stdout/stderr multiplexed.
		TTY bool
	}
)

// StreamContainerLogs streams Docker container logs, handling TTY raw streams
// and non-TTY multiplexed stdout/stderr streams.
func StreamContainerLogs(ctx context.Context, logs io.ReadCloser, logsChan chan<- string, opts LogOptions) error {
	if opts.TTY {
		return ReadLogLines(ctx, logs, logsChan, "")
	}
	if opts.Follow {
		return StreamMultiplexedLogs(ctx, logs, logsChan)
	}
	return ReadAllLogs(ctx, logs, logsChan)
}

// StreamMultiplexedLogs demultiplexes a Docker stdout/stderr log stream and
// sends non-empty lines to logsChan.
func StreamMultiplexedLogs(ctx context.Context, logs io.Reader, logsChan chan<- string) error {
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()

	var closeOnce sync.Once
	closeInputs := func() {
		closeOnce.Do(func() {
			err := cmp.Or(ctx.Err(), io.ErrClosedPipe)
			_ = stdoutReader.CloseWithError(err)
			_ = stderrReader.CloseWithError(err)
			if closer, ok := logs.(io.Closer); ok {
				_ = closer.Close()
			}
		})
	}

	copyDone := make(chan struct{})
	go func() {
		defer close(copyDone)
		defer func() { _ = stdoutWriter.Close() }()
		defer func() { _ = stderrWriter.Close() }()
		_, err := stdcopy.StdCopy(stdoutWriter, stderrWriter, logs)
		if err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
			slog.ErrorContext(ctx, "error demultiplexing logs", "error", err)
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			closeInputs()
		case <-copyDone:
		}
	}()

	done := make(chan error, 2)

	go func() {
		done <- ReadLogLines(ctx, stdoutReader, logsChan, "")
	}()

	go func() {
		done <- ReadLogLines(ctx, stderrReader, logsChan, "[STDERR] ")
	}()

	select {
	case <-ctx.Done():
		closeInputs()
		return ctx.Err()
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			closeInputs()
			return err
		}
		select {
		case <-ctx.Done():
			closeInputs()
			return ctx.Err()
		case err = <-done:
			if err != nil && !errors.Is(err, io.EOF) {
				closeInputs()
				return err
			}
			return nil
		}
	}
}

// ReadAllLogs reads a non-follow Docker multiplexed log stream and sends
// non-empty stdout/stderr lines to logsChan as they are demultiplexed, until EOF.
func ReadAllLogs(ctx context.Context, logs io.ReadCloser, logsChan chan<- string) error {
	return StreamMultiplexedLogs(ctx, logs, logsChan)
}

// maxLogLineBytes caps one log line so output without newlines cannot grow
// unbounded; longer runs are split into maxLogLineBytes chunks.
const maxLogLineBytes = 1 << 20

// ReadLogLines sends each non-empty line from reader to logsChan, prepending
// prefix, until EOF or ctx is done. Lines longer than 1 MiB are split.
func ReadLogLines(ctx context.Context, reader io.Reader, logsChan chan<- string, prefix string) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogLineBytes)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i], nil
		}
		if len(data) >= maxLogLineBytes {
			return maxLogLineBytes, data[:maxLogLineBytes], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimRight(scanner.Bytes(), "\r")
		if len(line) == 0 {
			continue
		}
		select {
		case logsChan <- prefix + string(line):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return scanner.Err()
}
