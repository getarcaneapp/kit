package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"syscall"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"kit.getarcane.app"
)

type (
	// ExecRequest describes one exec run by ExecInContainer.
	ExecRequest struct {
		// Stdout receives the exec's demultiplexed standard output.
		Stdout io.Writer
		// Stderr receives the exec's demultiplexed standard error.
		Stderr io.Writer
		// ContainerID is the container the exec runs in.
		ContainerID string
		// Options configures the exec, e.g. its command and environment.
		Options client.ExecCreateOptions
	}
)

var (
	// streamEndErrors are the typed errors that mark an ordinary end of a Docker
	// attach/log stream.
	streamEndErrors = []error{
		io.EOF,
		net.ErrClosed,
		context.Canceled,
		syscall.EPIPE,
		syscall.ECONNRESET,
	}

	// streamEndMessages match the same conditions once an error has crossed a
	// boundary that flattened it to text, such as the Docker API.
	streamEndMessages = []string{
		"use of closed network connection",
		"context canceled",
		"broken pipe",
		"connection reset by peer",
	}
)

// IsExpectedStreamEndError reports whether err is the ordinary way a Docker
// attach/log stream ends rather than a real failure.
func IsExpectedStreamEndError(err error) bool {
	if err == nil {
		return false
	}
	if slices.ContainsFunc(streamEndErrors, func(target error) bool {
		return errors.Is(err, target)
	}) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return slices.ContainsFunc(streamEndMessages, func(part string) bool {
		return strings.Contains(msg, part)
	})
}

// StartStdCopy demultiplexes a Docker stream into stdout/stderr on a goroutine
// and reports the copy result on the returned channel. Pair with WaitStdCopy.
func StartStdCopy(stream io.Reader, stdout, stderr io.Writer) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(stdout, stderr, stream)
		done <- err
	}()
	return done
}

// WaitStdCopy waits for a StartStdCopy goroutine, treating an ordinary stream
// end as success.
func WaitStdCopy(done <-chan error) error {
	err := <-done
	return kit.Ternary(err == nil || IsExpectedStreamEndError(err), nil, err)
}

// ExecInContainer runs req in its container, demultiplexing output into
// req.Stdout and req.Stderr, and returns the exec's exit code. A non-zero exit
// code is not an error — callers decide how to report it.
func ExecInContainer(ctx context.Context, dockerClient *client.Client, req ExecRequest) (int, error) {
	execResp, err := dockerClient.ExecCreate(ctx, req.ContainerID, req.Options)
	if err != nil {
		return 0, fmt.Errorf("failed to create exec: %w", err)
	}

	attachResp, err := dockerClient.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to attach to exec: %w", err)
	}
	defer attachResp.Close()
	stopClose := context.AfterFunc(ctx, attachResp.Close)
	defer stopClose()

	copyErr := WaitStdCopy(StartStdCopy(attachResp.Reader, req.Stdout, req.Stderr))
	if errErr := ctx.Err(); errErr != nil {
		return 0, fmt.Errorf("failed to read exec output: %w", errErr)
	}
	if copyErr != nil {
		return 0, fmt.Errorf("failed to read exec output: %w", copyErr)
	}

	inspect, err := dockerClient.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to inspect exec: %w", err)
	}

	return inspect.ExitCode, nil
}
