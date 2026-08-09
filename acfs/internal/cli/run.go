// Package cli implements the acfs command-line protocol.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"go.getarcane.app/acfs"
	"go.getarcane.app/acfs/internal/version"
	acfstypes "go.getarcane.app/acfs/types"
)

const (
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2
)

type pathFlags struct {
	root string
	path string
}

func newFlagSet(command string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func addPathFlags(flags *flag.FlagSet) *pathFlags {
	values := &pathFlags{}
	flags.StringVar(&values.root, "root", "", "workspace root directory")
	flags.StringVar(&values.path, "path", "", "logical path within the workspace")
	return values
}

func validatePathFlags(values *pathFlags) error {
	if values.root == "" {
		return errors.New("--root is required")
	}
	if values.path == "" {
		return errors.New("--path is required")
	}
	return nil
}

func parseMode(modeValue string) (os.FileMode, error) {
	parsed, err := strconv.ParseUint(modeValue, 8, 9)
	if err != nil {
		return 0, fmt.Errorf("invalid mode %q: %w", modeValue, err)
	}
	return os.FileMode(parsed), nil
}

func encodeJSON(destination io.Writer, value any) error {
	encoder := json.NewEncoder(destination)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func runList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("list", stderr)
	values := addPathFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("list does not accept positional arguments")
	}
	if err := validatePathFlags(values); err != nil {
		return err
	}
	temporaryOutput, err := os.CreateTemp("", "acfs-list-*")
	if err != nil {
		return fmt.Errorf("create list output: %w", err)
	}
	defer func() {
		_ = temporaryOutput.Close()
		_ = os.Remove(temporaryOutput.Name())
	}()
	bufferedOutput := bufio.NewWriterSize(temporaryOutput, 64*1024)

	started := false
	firstEntry := true
	var encodedEntry bytes.Buffer
	entryEncoder := json.NewEncoder(&encodedEntry)
	entryEncoder.SetEscapeHTML(false)
	err = acfs.ListEach(ctx, values.root, values.path, func(entry acfstypes.Entry) error {
		if !started {
			if _, err := io.WriteString(bufferedOutput, `{"entries":[`); err != nil {
				return fmt.Errorf("write list prefix: %w", err)
			}
			started = true
		}
		if !firstEntry {
			if _, err := io.WriteString(bufferedOutput, ","); err != nil {
				return fmt.Errorf("write list delimiter: %w", err)
			}
		}
		firstEntry = false

		encodedEntry.Reset()
		if err := entryEncoder.Encode(entry); err != nil {
			return fmt.Errorf("encode list entry: %w", err)
		}
		encoded := bytes.TrimSuffix(encodedEntry.Bytes(), []byte{'\n'})
		if _, err := bufferedOutput.Write(encoded); err != nil {
			return fmt.Errorf("write list entry: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !started {
		if _, err := io.WriteString(bufferedOutput, `{"entries":[`); err != nil {
			return fmt.Errorf("write list prefix: %w", err)
		}
	}
	if _, err := fmt.Fprintf(bufferedOutput, `],"version":%d}`+"\n", acfstypes.ProtocolVersion); err != nil {
		return fmt.Errorf("write list suffix: %w", err)
	}
	if err := bufferedOutput.Flush(); err != nil {
		return fmt.Errorf("flush list output: %w", err)
	}
	if _, err := temporaryOutput.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind list output: %w", err)
	}
	if _, err := io.Copy(stdout, temporaryOutput); err != nil {
		return fmt.Errorf("copy list output: %w", err)
	}
	return nil
}

func runStat(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("stat", stderr)
	values := addPathFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("stat does not accept positional arguments")
	}
	if err := validatePathFlags(values); err != nil {
		return err
	}

	entry, err := acfs.Stat(ctx, values.root, values.path)
	if err != nil {
		return err
	}
	return encodeJSON(stdout, acfstypes.StatResponse{
		Version: acfstypes.ProtocolVersion,
		Entry:   entry,
	})
}

func runWalk(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("walk", stderr)
	values := addPathFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("walk does not accept positional arguments")
	}
	if err := validatePathFlags(values); err != nil {
		return err
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	return acfs.Walk(ctx, values.root, values.path, func(entry acfstypes.Entry) error {
		return encoder.Encode(acfstypes.WalkRecord{
			Version: acfstypes.ProtocolVersion,
			Entry:   entry,
		})
	})
}

func runRead(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("read", stderr)
	values := addPathFlags(flags)
	limit := flags.Int64("limit", 0, "maximum bytes to read; zero reads the complete file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("read does not accept positional arguments")
	}
	if err := validatePathFlags(values); err != nil {
		return err
	}
	if *limit < 0 {
		return errors.New("--limit must be non-negative")
	}

	reader, size, err := acfs.OpenRead(ctx, values.root, values.path, *limit)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	if size < 0 {
		return errors.New("filesystem returned a negative file size")
	}

	// OpenRead obtains size from os.FileInfo and rejects directories, so the
	// checked value is safe to represent in the protocol's unsigned field.
	if err := acfs.WriteStreamHeader(stdout, uint64(size)); err != nil {
		return err
	}
	written, err := io.Copy(stdout, reader)
	if err != nil {
		return fmt.Errorf("stream file: %w", err)
	}
	if written != size {
		return fmt.Errorf("streamed %d bytes, expected %d", written, size)
	}
	return nil
}

func runWrite(ctx context.Context, args []string, stdin io.Reader, stderr io.Writer) error {
	flags := newFlagSet("write", stderr)
	values := addPathFlags(flags)
	size := flags.Int64("size", -1, "exact number of bytes to read from stdin")
	modeValue := flags.String("mode", "0644", "destination mode in octal")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("write does not accept positional arguments")
	}
	if err := validatePathFlags(values); err != nil {
		return err
	}
	if *size < 0 {
		return errors.New("--size is required and must be non-negative")
	}
	mode, err := parseMode(*modeValue)
	if err != nil {
		return err
	}

	_, err = acfs.WriteFrom(ctx, values.root, values.path, stdin, *size, mode)
	return err
}

func runMkdir(ctx context.Context, args []string, stderr io.Writer) error {
	flags := newFlagSet("mkdir", stderr)
	values := addPathFlags(flags)
	modeValue := flags.String("mode", "0755", "directory mode in octal")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("mkdir does not accept positional arguments")
	}
	if err := validatePathFlags(values); err != nil {
		return err
	}
	mode, err := parseMode(*modeValue)
	if err != nil {
		return err
	}
	return acfs.MkdirAll(ctx, values.root, values.path, mode)
}

func runRemove(ctx context.Context, args []string, stderr io.Writer) error {
	flags := newFlagSet("remove", stderr)
	values := addPathFlags(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("remove does not accept positional arguments")
	}
	if err := validatePathFlags(values); err != nil {
		return err
	}
	return acfs.RemoveAll(ctx, values.root, values.path)
}

func runVersion(args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("version", stderr)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("version does not accept positional arguments")
	}
	return encodeJSON(stdout, acfstypes.VersionResponse{
		Version:   version.Version,
		Revision:  version.Revision,
		BuildTime: version.BuildTime,
		Protocol:  acfstypes.ProtocolVersion,
	})
}

func printUsage(stderr io.Writer) {
	_, _ = fmt.Fprintln(stderr, "usage: acfs <list|walk|stat|read|write|mkdir|remove|version> [options]")
}

// Run executes acfs and returns its process exit code.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}

	command := args[0]
	var err error
	switch command {
	case "list":
		err = runList(ctx, args[1:], stdout, stderr)
	case "walk":
		err = runWalk(ctx, args[1:], stdout, stderr)
	case "stat":
		err = runStat(ctx, args[1:], stdout, stderr)
	case "read":
		err = runRead(ctx, args[1:], stdout, stderr)
	case "write":
		err = runWrite(ctx, args[1:], stdin, stderr)
	case "mkdir":
		err = runMkdir(ctx, args[1:], stderr)
	case "remove":
		err = runRemove(ctx, args[1:], stderr)
	case "version":
		err = runVersion(args[1:], stdout, stderr)
	default:
		printUsage(stderr)
		return exitUsage
	}

	if err != nil {
		_, _ = fmt.Fprintf(stderr, "acfs: %s: %v\n", command, err)
		return exitFailure
	}
	return exitSuccess
}
