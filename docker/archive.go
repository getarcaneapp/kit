package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"slices"

	"github.com/moby/moby/client"
)

type (
	archiveFile struct {
		io.Reader
		io.Closer
	}
)

var ErrArchiveDirectory = errors.New("path is a directory")

func CopyToContainer(ctx context.Context, dockerClient *client.Client, containerID, destDir string, files map[string][]byte) error {
	if len(files) == 0 {
		return nil
	}
	archive, err := tarFiles(files)
	if err != nil {
		return fmt.Errorf("build archive: %w", err)
	}
	if _, copyErr := dockerClient.CopyToContainer(ctx, containerID, client.CopyToContainerOptions{
		DestinationPath: destDir,
		Content:         archive,
	}); copyErr != nil {
		return fmt.Errorf("copy archive to container: %w", copyErr)
	}
	return nil
}

func CopyFromContainer(ctx context.Context, dockerClient *client.Client, containerID, sourcePath string) (io.ReadCloser, int64, error) {
	result, err := dockerClient.CopyFromContainer(ctx, containerID, client.CopyFromContainerOptions{SourcePath: sourcePath})
	if err != nil {
		return nil, 0, fmt.Errorf("copy archive from container: %w", err)
	}
	return firstArchiveFile(result.Content)
}

func tarFiles(files map[string][]byte) (*bytes.Buffer, error) {
	archive := &bytes.Buffer{}
	writer := tar.NewWriter(archive)
	written := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		var parents []string
		for dir := path.Dir(name); dir != "." && dir != "/" && !written[dir]; dir = path.Dir(dir) {
			written[dir] = true
			parents = append(parents, dir)
		}
		for _, dir := range slices.Backward(parents) {
			if err := writer.WriteHeader(&tar.Header{
				Name:     dir + "/",
				Mode:     0o755,
				Typeflag: tar.TypeDir,
			}); err != nil {
				return nil, fmt.Errorf("write directory header for %s: %w", dir, err)
			}
		}
		if err := writer.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(files[name])),
			Typeflag: tar.TypeReg,
		}); err != nil {
			return nil, fmt.Errorf("write file header for %s: %w", name, err)
		}
		if _, err := writer.Write(files[name]); err != nil {
			return nil, fmt.Errorf("write file %s: %w", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close archive: %w", err)
	}
	return archive, nil
}

func firstArchiveFile(archive io.ReadCloser) (io.ReadCloser, int64, error) {
	reader := tar.NewReader(archive)
	header, err := reader.Next()
	if err != nil {
		_ = archive.Close()
		return nil, 0, fmt.Errorf("read archive header: %w", err)
	}
	if header.FileInfo().IsDir() {
		_ = archive.Close()
		return nil, 0, ErrArchiveDirectory
	}
	return archiveFile{
		Reader: reader,
		Closer: archive,
	}, header.Size, nil
}
