package acfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	acfstypes "go.getarcane.app/acfs/types"
)

const (
	// StreamHeaderSize is the number of bytes in an acfs read header.
	StreamHeaderSize = 16
)

var streamMagic = [4]byte{'A', 'R', 'C', 'W'}

// WriteStreamHeader writes an acfs stream header.
func WriteStreamHeader(destination io.Writer, payloadLength uint64) error {
	var header [StreamHeaderSize]byte
	copy(header[:4], streamMagic[:])
	header[4] = byte(acfstypes.ProtocolVersion)
	binary.BigEndian.PutUint64(header[8:], payloadLength)
	if _, err := destination.Write(header[:]); err != nil {
		return fmt.Errorf("write stream header: %w", err)
	}
	return nil
}

// ReadStreamHeader validates an acfs stream header and
// returns its payload length.
func ReadStreamHeader(source io.Reader) (uint64, error) {
	var header [StreamHeaderSize]byte
	if _, err := io.ReadFull(source, header[:]); err != nil {
		return 0, fmt.Errorf("read stream header: %w", err)
	}
	if string(header[:4]) != string(streamMagic[:]) {
		return 0, errors.New("invalid stream header magic")
	}
	if header[4] != byte(acfstypes.ProtocolVersion) {
		return 0, fmt.Errorf("unsupported stream protocol %d", header[4])
	}
	if header[5] != 0 || header[6] != 0 || header[7] != 0 {
		return 0, errors.New("invalid stream header reserved bytes")
	}
	return binary.BigEndian.Uint64(header[8:]), nil
}
