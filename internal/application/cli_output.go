package application

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"safepatch/internal/domain/feature"
)

type CLIErrorCode string

const (
	CodeFeatureNotEntitled     CLIErrorCode = "FEATURE_NOT_ENTITLED"
	CodeUnknownFeature         CLIErrorCode = "UNKNOWN_FEATURE"
	CodeInvalidUsage           CLIErrorCode = "INVALID_USAGE"
	CodeEntitlementUnavailable CLIErrorCode = "ENTITLEMENT_UNAVAILABLE"
	CodeNotImplemented         CLIErrorCode = "NOT_IMPLEMENTED"
	CodeOutputError            CLIErrorCode = "OUTPUT_ERROR"
)

// CLIError is a stable transport error. Messages are application-owned, never
// raw adapter errors, credentials or unvalidated arguments. Causes stay private.
type CLIError struct {
	Code        CLIErrorCode            `json:"code"`
	Message     string                  `json:"message"`
	Feature     feature.Key             `json:"feature,omitempty"`
	Entitlement *EntitlementExplanation `json:"entitlement,omitempty"`
	cause       error
}

func (e *CLIError) Error() string { return string(e.Code) + ": " + e.Message }
func (e *CLIError) Unwrap() error { return e.cause }

type cliOutput struct {
	stdout, stderr io.Writer
	json           bool
}

// JSON success goes to stdout; failure is one error envelope on stderr. No
// ANSI, prompts, upgrade links or sales messages are emitted, including in CI.
func (o cliOutput) failure(err *CLIError, exitCode int) int {
	var writeErr error
	if o.stderr == nil {
		return 1
	}
	if o.json {
		writeErr = writeCLIJSON(o.stderr, struct {
			Error *CLIError `json:"error"`
		}{err}, false)
	} else {
		_, writeErr = fmt.Fprintln(o.stderr, err.Error())
		if writeErr == nil && err.Entitlement != nil {
			_, writeErr = io.WriteString(o.stderr, explainText(*err.Entitlement))
		}
	}
	if writeErr != nil {
		return 1
	}
	return exitCode
}

func (o cliOutput) success(value any, plain string) int {
	var err error
	if o.stdout == nil {
		err = io.ErrClosedPipe
	} else if o.json {
		err = writeCLIJSON(o.stdout, value, true)
	} else {
		var n int
		n, err = io.WriteString(o.stdout, plain)
		if err == nil && n != len(plain) {
			err = io.ErrShortWrite
		}
	}
	if err != nil {
		return o.failure(&CLIError{Code: CodeOutputError, Message: "could not write command output", cause: err}, 1)
	}
	return 0
}

func (o cliOutput) help(usage string) int {
	return o.success(struct {
		Usage string `json:"usage"`
	}{strings.TrimSpace(usage)}, usage)
}

// Recognize --json once at any position, including for invalid command usage.
func cliArguments(args []string) (positionals []string, jsonMode bool, valid bool) {
	valid = true
	for _, arg := range args {
		if arg == "--json" {
			if jsonMode {
				valid = false
			}
			jsonMode = true
		} else {
			positionals = append(positionals, arg)
		}
	}
	return
}

func helpArgument(arg string) bool { return arg == "--help" || arg == "-h" || arg == "help" }

func writeCLIJSON(writer io.Writer, value any, pretty bool) error {
	var data []byte
	var err error
	if pretty {
		data, err = json.MarshalIndent(value, "", "  ")
	} else {
		data, err = json.Marshal(value)
	}
	if err != nil {
		return err
	}
	data = append(data, '\n')
	n, err := writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}
