package acfs

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestStreamHeader(t *testing.T) {
	t.Parallel()

	var destination bytes.Buffer
	const payloadLength = uint64(0x0102030405060708)
	if err := WriteStreamHeader(&destination, payloadLength); err != nil {
		t.Fatal(err)
	}
	want := []byte{'A', 'R', 'C', 'W', 1, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	if !bytes.Equal(destination.Bytes(), want) {
		t.Fatalf("header = %v, want %v", destination.Bytes(), want)
	}
	got, err := ReadStreamHeader(bytes.NewReader(destination.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got != payloadLength {
		t.Fatalf("payload length = %#x, want %#x", got, payloadLength)
	}
}

func TestReadStreamHeaderRejectsInvalidHeaders(t *testing.T) {
	t.Parallel()

	valid := make([]byte, StreamHeaderSize)
	copy(valid, "ARCW")
	valid[4] = 1
	binary.BigEndian.PutUint64(valid[8:], 42)

	tests := map[string][]byte{
		"short":    valid[:8],
		"magic":    append([]byte(nil), valid...),
		"version":  append([]byte(nil), valid...),
		"reserved": append([]byte(nil), valid...),
	}
	tests["magic"][0] = 'X'
	tests["version"][4] = 2
	tests["reserved"][7] = 1
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadStreamHeader(bytes.NewReader(header)); err == nil {
				t.Fatal("ReadStreamHeader unexpectedly succeeded")
			}
		})
	}
}
