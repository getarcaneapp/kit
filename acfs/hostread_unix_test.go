//go:build unix

package acfs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// feedFIFO writes content to the FIFO at name once a reader opens it.
func feedFIFO(t *testing.T, name, content string) {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() {
		<-done
	})
	go func() {
		defer close(done)
		writer, err := os.OpenFile(name, os.O_WRONLY, 0)
		if err != nil {
			t.Error(err)
			return
		}
		_, err = writer.WriteString(content)
		err = errors.Join(err, writer.Close())
		if err != nil {
			t.Error(err)
		}
	}()
}

func TestHostSpecialReadsFIFO(t *testing.T) {
	t.Parallel()

	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := (Host{}).ReadFile(t.Context(), fifo); !errors.Is(err, ErrNotFile) {
		t.Fatalf("Host{}.ReadFile(fifo) = %v, want ErrNotFile", err)
	}
	if _, err := (Host{Special: true}).ReadFile(t.Context(), filepath.Dir(fifo)); !errors.Is(err, ErrIsDirectory) {
		t.Fatalf("Host{Special: true}.ReadFile(directory) = %v, want ErrIsDirectory", err)
	}

	feedFIFO(t, fifo, "piped content")
	content, err := Host{Special: true}.ReadFile(t.Context(), fifo)
	if err != nil {
		t.Fatalf("ReadFile(fifo): %v", err)
	}
	if string(content) != "piped content" {
		t.Fatalf("ReadFile(fifo) = %q, want piped content", content)
	}

	feedFIFO(t, fifo, "streamed")
	reader, size, err := Host{Special: true}.OpenRead(t.Context(), fifo, 0)
	if err != nil {
		t.Fatalf("OpenRead(fifo): %v", err)
	}
	var streamed bytes.Buffer
	_, err = streamed.ReadFrom(reader)
	err = errors.Join(err, reader.Close())
	if err != nil || size != -1 || streamed.String() != "streamed" {
		t.Fatalf("OpenRead(fifo) = (%q, %d, %v), want (streamed, -1, nil)", streamed.String(), size, err)
	}
}

func TestHostSpecialOpenFileWritesFIFO(t *testing.T) {
	t.Parallel()

	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (Host{}).OpenFile(t.Context(), fifo, OpenOptions{Write: true}); !errors.Is(err, ErrNotFile) {
		t.Fatalf("Host{}.OpenFile(fifo) = %v, want ErrNotFile", err)
	}

	received := make(chan string, 1)
	go func() {
		content, err := os.ReadFile(fifo)
		if err != nil {
			t.Error(err)
		}
		received <- string(content)
	}()
	file, err := Host{Special: true}.OpenFile(t.Context(), fifo, OpenOptions{Write: true, Truncate: true})
	if err != nil {
		t.Fatalf("OpenFile(fifo): %v", err)
	}
	_, err = file.WriteString("output")
	err = errors.Join(err, file.Close())
	if err != nil {
		t.Fatal(err)
	}
	if got := <-received; got != "output" {
		t.Fatalf("fifo received %q, want output", got)
	}
}
