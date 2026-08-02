// Package exec defines the single boundary through which pr-buddy shells out.
// Every external command goes through Runner, so tests can substitute a fake
// and no code path can quietly execute something unreviewed.
package exec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes an external command and returns its stdout.
type Runner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

// Error carries the failing command and its stderr, which is usually the only
// useful part of a git or gh failure.
type Error struct {
	Cmd    string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("%s: %v: %s", e.Cmd, e.Err, strings.TrimSpace(e.Stderr))
	}
	return fmt.Sprintf("%s: %v", e.Cmd, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Real runs commands for actual. It never invokes a shell, so no argument can
// be interpreted as shell syntax.
type Real struct{}

func (Real) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// Untrusted repositories must not influence our own subprocesses through
	// the environment; we inherit deliberately and add nothing.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), &Error{
			Cmd:    name + " " + strings.Join(args, " "),
			Stderr: stderr.String(),
			Err:    err,
		}
	}
	return stdout.String(), nil
}
