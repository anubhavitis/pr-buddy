package exec

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Call records one invocation made through a Fake.
type Call struct {
	Dir  string
	Name string
	Args []string
}

// String renders a call as the command line it represents.
func (c Call) String() string {
	return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " "))
}

// Response is a canned result for a matched command.
type Response struct {
	Stdout string
	Stderr string
	Err    error
}

// Fake is a Runner that returns canned responses and records every call. It is
// the test double for the process boundary; no test should ever shell out for
// real.
type Fake struct {
	mu sync.Mutex
	// Responses maps a substring of the command line to its response. The
	// longest matching key wins, so specific matches beat general ones.
	Responses map[string]Response
	// Default is returned when no key matches.
	Default Response
	// Calls records invocations in order.
	Calls []Call
}

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{Responses: map[string]Response{}}
}

// Respond registers a response for command lines containing match.
func (f *Fake) Respond(match string, r Response) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Responses == nil {
		f.Responses = map[string]Response{}
	}
	f.Responses[match] = r
	return f
}

// RespondOK registers successful stdout for command lines containing match.
func (f *Fake) RespondOK(match, stdout string) *Fake {
	return f.Respond(match, Response{Stdout: stdout})
}

func (f *Fake) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	call := Call{Dir: dir, Name: name, Args: args}

	f.mu.Lock()
	f.Calls = append(f.Calls, call)
	line := call.String()
	var best string
	for k := range f.Responses {
		if strings.Contains(line, k) && len(k) > len(best) {
			best = k
		}
	}
	resp := f.Default
	if best != "" {
		resp = f.Responses[best]
	}
	f.mu.Unlock()

	if resp.Err != nil {
		return resp.Stdout, &Error{Cmd: line, Stderr: resp.Stderr, Err: resp.Err}
	}
	return resp.Stdout, nil
}

// CommandLines returns every recorded call as a command line.
func (f *Fake) CommandLines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.Calls))
	for _, c := range f.Calls {
		out = append(out, c.String())
	}
	return out
}

// Ran reports whether any recorded call contains match.
func (f *Fake) Ran(match string) bool {
	for _, line := range f.CommandLines() {
		if strings.Contains(line, match) {
			return true
		}
	}
	return false
}

// Count returns how many recorded calls contain match.
func (f *Fake) Count(match string) int {
	n := 0
	for _, line := range f.CommandLines() {
		if strings.Contains(line, match) {
			n++
		}
	}
	return n
}

// Reset clears recorded calls, keeping registered responses.
func (f *Fake) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = nil
}

// ErrExit is a stand-in for a non-zero command exit.
var ErrExit = fmt.Errorf("exit status 1")
