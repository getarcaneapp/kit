package types

import (
	"errors"
	"fmt"

	composegotypes "github.com/compose-spec/compose-go/v2/types"
)

type Options struct {
	ExistingComposeYAML []byte
	RenderWarnings      bool
}

type ParseOptions struct{}

type MarshalOptions struct {
	RenderWarnings bool
}

type Result struct {
	YAML     []byte
	Project  *composegotypes.Project
	Services []ServiceResult
	EnvFile  []byte
	Warnings []Warning
}

type ServiceResult struct {
	Name  string
	Image string
}

type Warning struct {
	Message string
}

type RunCommand struct {
	Image      string
	Name       string
	Command    []string
	Entrypoint string
	Flags      []Flag
}

type Flag struct {
	Name  string
	Value string
}

type Document struct {
	Services     map[string]Service
	ServiceOrder []string
	Networks     map[string]map[string]any
	Volumes      map[string]map[string]any
	Warnings     []Warning
}

type Service map[string]any

var (
	ErrParse      = errors.New("parse docker command")
	ErrConversion = errors.New("convert docker command")
)

type ParseError struct {
	Message string
}

func (e *ParseError) Error() string {
	return e.Message
}

func (e *ParseError) Unwrap() error {
	return ErrParse
}

type ConversionError struct {
	Message string
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
