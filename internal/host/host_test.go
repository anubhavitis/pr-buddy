package host

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

func TestCompleteClaudeUsesPrintModeWithoutTools(t *testing.T) {
	fake := xexec.NewFake().RespondOK("claude", `{"groups":[]}`)
	c := &Completer{Exec: fake}

	text, err := c.Complete(context.Background(), Request{
		Backend: BackendClaude,
		Prompt:  "order these files",
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != `{"groups":[]}` {
		t.Fatalf("got %q", text)
	}

	line := fake.CommandLines()[0]
	for _, want := range []string{"claude", "-p", "--output-format", "text", `--tools`, "--permission-mode", "dontAsk", "order these files"} {
		if !strings.Contains(line, want) {
			t.Errorf("command %q missing %q", line, want)
		}
	}
	if strings.Contains(line, "--bare") {
		t.Fatal("--bare skips keychain login")
	}
	if strings.Contains(line, "dangerously-skip-permissions") {
		t.Fatal("must not skip permissions")
	}
	if strings.Contains(line, "--model") {
		t.Fatal("empty model must not pass --model")
	}
}

func TestCompleteClaudeIgnoresUndefinedModel(t *testing.T) {
	fake := xexec.NewFake().RespondOK("claude", `{"groups":[]}`)
	c := &Completer{Exec: fake}
	if _, err := c.Complete(context.Background(), Request{
		Backend: BackendClaude,
		Prompt:  "order these files",
		Model:   "undefined",
	}); err != nil {
		t.Fatal(err)
	}
	line := fake.CommandLines()[0]
	if strings.Contains(line, "--model") {
		t.Fatalf("bogus model must not pass --model, got %q", line)
	}
}

func TestCompleteClaudePassesModelWhenSet(t *testing.T) {
	fake := xexec.NewFake().RespondOK("claude", `{"groups":[]}`)
	c := &Completer{Exec: fake}
	if _, err := c.Complete(context.Background(), Request{
		Backend: BackendClaude,
		Prompt:  "order these files",
		Model:   "opus",
	}); err != nil {
		t.Fatal(err)
	}
	line := fake.CommandLines()[0]
	if !strings.Contains(line, "--model opus") && !strings.Contains(line, "--model\topus") {
		if !strings.Contains(line, "--model") || !strings.Contains(line, "opus") {
			t.Fatalf("command %q missing --model opus", line)
		}
	}
}

func TestGuideSchemaIsTheGrokContract(t *testing.T) {
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(grokGuideSchema), &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "groups" {
		t.Fatalf("required %v", schema.Required)
	}
	if schema.Properties["groups"].Type != "array" {
		t.Fatalf("groups type %q", schema.Properties["groups"].Type)
	}
}

func TestCompleteGrokUsesSingleTurn(t *testing.T) {
	fake := xexec.NewFake().RespondOK("grok", "ok")
	c := &Completer{Exec: fake}

	if _, err := c.Complete(context.Background(), Request{
		Backend: BackendGrok,
		Prompt:  "order these files",
	}); err != nil {
		t.Fatal(err)
	}

	line := fake.CommandLines()[0]
	for _, want := range []string{"grok", "-p", "order these files", "--output-format", "json", "--json-schema", "--disable-web-search", "--permission-mode", "dontAsk"} {
		if !strings.Contains(line, want) {
			t.Errorf("command %q missing %q", line, want)
		}
	}
	if strings.Contains(line, " -m ") || strings.HasSuffix(line, " -m") {
		t.Fatal("empty model must not pass -m")
	}
}

func TestCompleteGrokPassesModelWhenSet(t *testing.T) {
	fake := xexec.NewFake().RespondOK("grok", "ok")
	c := &Completer{Exec: fake}
	if _, err := c.Complete(context.Background(), Request{
		Backend: BackendGrok,
		Prompt:  "order these files",
		Model:   "grok-4.6",
	}); err != nil {
		t.Fatal(err)
	}
	line := fake.CommandLines()[0]
	if !strings.Contains(line, "-m grok-4.6") && !strings.Contains(line, "-m\tgrok-4.6") {
		if !strings.Contains(line, "-m") || !strings.Contains(line, "grok-4.6") {
			t.Fatalf("command %q missing -m grok-4.6", line)
		}
	}
}

func TestCompleteGrokUnwrapsStructuredOutput(t *testing.T) {
	fake := xexec.NewFake().RespondOK("grok", `{"text":"ignore","structuredOutput":{"groups":[{"name":"A","summary":"s","files":[{"path":"a.go","blurb":"b"}]}]}}`)
	c := &Completer{Exec: fake}
	text, err := c.Complete(context.Background(), Request{Backend: BackendGrok, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `"groups"`) || !strings.Contains(text, `a.go`) {
		t.Fatalf("unwrapped %s", text)
	}
}

func TestCompleteMLXPostsChatCompletions(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"{\"groups\":[]}"}}]}`)
	}))
	defer srv.Close()

	c := &Completer{HTTP: srv.Client()}
	text, err := c.Complete(context.Background(), Request{
		Backend:  BackendMLX,
		Prompt:   "order these files",
		MLXURL:   srv.URL + "/v1",
		MLXModel: "qwen-local",
	})
	if err != nil {
		t.Fatal(err)
	}
	if text != `{"groups":[]}` {
		t.Fatalf("got %q", text)
	}
	if gotBody["model"] != "qwen-local" {
		t.Fatalf("model %v", gotBody["model"])
	}
}

func TestCompleteMLXRejectsNonLoopback(t *testing.T) {
	c := &Completer{HTTP: http.DefaultClient}
	_, err := c.Complete(context.Background(), Request{
		Backend:  BackendMLX,
		Prompt:   "x",
		MLXURL:   "https://example.com/v1",
		MLXModel: "m",
	})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("want loopback error, got %v", err)
	}
}

func TestCompleteRejectsEmptyPromptAndUnknownBackend(t *testing.T) {
	c := &Completer{Exec: xexec.NewFake()}
	if _, err := c.Complete(context.Background(), Request{Backend: BackendClaude}); err == nil {
		t.Fatal("empty prompt should fail")
	}
	if _, err := c.Complete(context.Background(), Request{Backend: "openai", Prompt: "x"}); err == nil {
		t.Fatal("unknown backend should fail")
	}
}

func TestCompleteSurfacesCLIFailure(t *testing.T) {
	fake := xexec.NewFake().Respond("claude", xexec.Response{
		Stderr: "not logged in",
		Err:    xexec.ErrExit,
	})
	c := &Completer{Exec: fake}
	_, err := c.Complete(context.Background(), Request{Backend: BackendClaude, Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "-p") {
		t.Fatalf("error leaked argv: %v", err)
	}
}

func TestHandlerCompleteRoundTrip(t *testing.T) {
	fake := xexec.NewFake().RespondOK("claude", `{"groups":[]}`)
	srv := httptest.NewServer(Handler(&Completer{Exec: fake}))
	defer srv.Close()

	body := `{"backend":"claude","prompt":"order these files"}`
	res, err := http.Post(srv.URL+"/complete", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	var out completeResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Text != `{"groups":[]}` {
		t.Fatalf("%+v", out)
	}
}

func TestHandlerAllowsChromeExtensionOrigin(t *testing.T) {
	srv := httptest.NewServer(Handler(&Completer{Exec: xexec.NewFake()}))
	defer srv.Close()
	req, err := http.NewRequest(http.MethodOptions, srv.URL+"/complete", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "chrome-extension://abcdefgh")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", res.StatusCode)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "chrome-extension://abcdefgh" {
		t.Fatalf("cors %q", got)
	}
}

func TestHandlerHealth(t *testing.T) {
	srv := httptest.NewServer(Handler(&Completer{Exec: xexec.NewFake()}))
	defer srv.Close()
	res, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestChatCompletionsURL(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"http://127.0.0.1:8080/v1", "http://127.0.0.1:8080/v1/chat/completions"},
		{"http://127.0.0.1:8080/v1/", "http://127.0.0.1:8080/v1/chat/completions"},
		{"http://localhost:8080/v1/chat/completions", "http://localhost:8080/v1/chat/completions"},
	}
	for _, tt := range tests {
		got, err := chatCompletionsURL(tt.in)
		if err != nil {
			t.Fatalf("%s: %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("%s: got %s want %s", tt.in, got, tt.want)
		}
	}
}

func TestCompleteContextCanceled(t *testing.T) {
	fake := xexec.NewFake()
	c := &Completer{Exec: fake}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Complete(ctx, Request{Backend: BackendClaude, Prompt: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
