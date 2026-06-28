package api

import (
	"encoding/json"
	"io"

	"go.getarcane.app/builds/types"
)

type flusher interface{ Flush() }

func writeProgressEventInternal(w io.Writer, event types.ProgressEvent) {
	if w == nil {
		return
	}
	data, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = w.Write(append(data, '\n'))
	if f, ok := w.(flusher); ok {
		f.Flush()
	}
}
