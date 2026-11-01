package docker

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

type (
	closeTracker struct {
		io.Reader
		closed bool
	}
)

func (c *closeTracker) Close() error {
	c.closed = true
	return nil
}

func TestTarFilesWritesParentsOnceInSortedOrder(t *testing.T) {
	archive, err := tarFiles(map[string][]byte{
		"b.txt":         []byte("b"),
		"dir/sub/c.txt": []byte("c"),
		"dir/a.txt":     []byte("a"),
	})
	require.NoError(t, err)

	reader := tar.NewReader(archive)
	var entries []string
	contents := map[string]string{}
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		require.NoError(t, nextErr)
		entries = append(entries, header.Name)
		if header.Typeflag == tar.TypeReg {
			require.Equal(t, int64(0o644), header.Mode)
			content, readErr := io.ReadAll(reader)
			require.NoError(t, readErr)
			contents[header.Name] = string(content)
		} else {
			require.Equal(t, int64(0o755), header.Mode)
		}
	}

	require.Equal(t, []string{"b.txt", "dir/", "dir/a.txt", "dir/sub/", "dir/sub/c.txt"}, entries)
	require.Equal(t, map[string]string{"b.txt": "b", "dir/a.txt": "a", "dir/sub/c.txt": "c"}, contents)
}

func TestFirstArchiveFileStreamsFirstEntry(t *testing.T) {
	archive, err := tarFiles(map[string][]byte{"report.json": []byte(`{"ok":true}`)})
	require.NoError(t, err)
	source := &closeTracker{Reader: archive}

	file, size, err := firstArchiveFile(source)
	require.NoError(t, err)
	require.Equal(t, int64(len(`{"ok":true}`)), size)
	content, err := io.ReadAll(file)
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true}`, string(content))
	require.NoError(t, file.Close())
	require.True(t, source.closed)
}

func TestFirstArchiveFileRejectsDirectory(t *testing.T) {
	archive, err := tarFiles(map[string][]byte{"dir/file": nil})
	require.NoError(t, err)
	source := &closeTracker{Reader: archive}

	_, _, err = firstArchiveFile(source)
	require.ErrorIs(t, err, ErrArchiveDirectory)
	require.True(t, source.closed)
}

func TestFirstArchiveFileRejectsEmptyArchive(t *testing.T) {
	source := &closeTracker{Reader: &bytes.Buffer{}}

	_, _, err := firstArchiveFile(source)
	require.ErrorIs(t, err, io.EOF)
	require.True(t, source.closed)
}
