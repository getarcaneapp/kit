package types

import (
	"errors"
	"fmt"

	composegotypes "github.com/compose-spec/compose-go/v2/types"
)

type (
	// Options configures Convert and Build. Its MarshalOptions are applied when
	// Convert renders the document.
	Options struct {
		// ExistingComposeYAML is a compose file the converted services are merged into.
		ExistingComposeYAML []byte
		MarshalOptions
	}

	// MarshalOptions configures Marshal.
	MarshalOptions struct {
		// RenderWarnings appends conversion warnings to the YAML as comments.
		RenderWarnings bool
	}

	Result struct {
		YAML     []byte
		Project  *composegotypes.Project
		Services []ServiceResult
		EnvFile  []byte
		Warnings []Warning
	}

	ServiceResult struct {
		Name  string
		Image string
	}

	Warning struct {
		Message string
	}

	RunCommand struct {
		Image      string
		Name       string
		Command    []string
		Entrypoint string
		Flags      []Flag
	}

	Flag struct {
		Name  string
		Value string
	}

	Document struct {
		Services     map[string]Service
		ServiceOrder []string
		Networks     map[string]map[string]any
		Volumes      map[string]map[string]any
		// Sections holds the other top-level keys of Options.ExistingComposeYAML,
		// such as secrets, configs and x-* extensions.
		Sections map[string]any
		Warnings []Warning
	}

	Service map[string]any

	ParseError struct {
		Message string
	}

	ConversionError struct {
		Message string
	}
)

var (
	ErrParse      = errors.New("parse docker command")
	ErrConversion = errors.New("convert docker command")
)

func (e *ParseError) Error() string {
	return e.Message
}

func (e *ParseError) Unwrap() error {
	return ErrParse
}

func (e *ConversionError) Error() string {
	return e.Message
}

func (e *ConversionError) Unwrap() error {
	return ErrConversion
}

func NewParseError(format string, args ...any) error {
	return &ParseError{Message: fmt.Sprintf(format, args...)}
}

func NewConversionError(format string, args ...any) error {
	return &ConversionError{Message: fmt.Sprintf(format, args...)}
}
