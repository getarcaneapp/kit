package api

import (
	"context"
	json "encoding/json/v2"
	"io"
	"sync"

	controlapi "github.com/moby/buildkit/api/services/control"
	buildkit "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client/pkg/jsonmessage"
)

// syncWriter serializes writes to a caller-provided writer that is shared
// between concurrent producers. Callers hand BuildImage a plain io.Writer and
// must not be required to make it concurrency-safe themselves.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// streamSolveStatusInternal renders BuildKit solve progress exactly as
// `docker buildx build --progress=plain` prints it. It consumes ch until it
// is closed, keeping the solve unblocked even if rendering fails.
//
// ch is bidirectional because progressui's Display.UpdateFrom requires a
// bidirectional channel; this function only receives from it.
func streamSolveStatusInternal(ctx context.Context, ch chan *buildkit.SolveStatus, w io.Writer) error {
	if w == nil {
		w = io.Discard
	}
	display, err := progressui.NewDisplay(w, progressui.PlainMode)
	if err != nil {
		for status := range ch {
			// Drain so the solve is never blocked on the status channel.
			_ = status
		}
		return err
	}
	_, err = display.UpdateFrom(ctx, ch)
	return err
}

// renderDockerBuildStreamInternal renders a daemon /build response as the raw
// text the docker CLI prints in non-TTY mode. BuildKit trace aux frames are
// decoded into solve statuses and rendered in plain progress mode; everything
// else (classic status/stream lines, errors) renders via jsonmessage.
func renderDockerBuildStreamInternal(ctx context.Context, reader io.Reader, w io.Writer) error {
	if w == nil {
		w = io.Discard
	}
	// The progress display goroutine and the jsonmessage renderer below write
	// to w concurrently; serialize so callers can pass any plain writer.
	w = &syncWriter{w: w}

	statusCh := make(chan *buildkit.SolveStatus, 16)
	displayDone := make(chan error, 1)
	go func() {
		displayDone <- streamSolveStatusInternal(ctx, statusCh, w)
	}()

	aux := func(msg jsonstream.Message) {
		if msg.ID != "moby.buildkit.trace" || msg.Aux == nil {
			return
		}
		var dt []byte
		if err := json.Unmarshal(*msg.Aux, &dt); err != nil {
			return
		}
		var resp controlapi.StatusResponse
		if err := resp.UnmarshalVT(dt); err != nil {
			return
		}
		select {
		case statusCh <- buildkit.NewSolveStatus(&resp):
		case <-ctx.Done():
		}
	}

	err := jsonmessage.DisplayJSONMessagesStream(reader, w, 0, false, aux)
	close(statusCh)
	<-displayDone
	return err
}
