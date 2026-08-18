package host

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

//go:embed guide.schema.json
var grokGuideSchema string

type Backend string

const (
	BackendClaude Backend = "claude"
	BackendGrok   Backend = "grok"
	BackendMLX    Backend = "mlx"
)

type Request struct {
	Backend  Backend
	Prompt   string
	MLXURL   string
	MLXModel string
}

type Completer struct {
	Exec execRunner
	HTTP *http.Client
}

type execRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
}

func (c *Completer) Complete(ctx context.Context, req Request) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return "", fmt.Errorf("prompt is empty")
	}
	switch req.Backend {
	case BackendClaude:
		// Do not pass --bare: it skips keychain OAuth and forces ANTHROPIC_API_KEY.
		return c.runCLI(ctx, "claude",
			"-p",
			"--output-format", "text",
			"--tools", "",
			"--permission-mode", "dontAsk",
			req.Prompt,
		)
	case BackendGrok:
		return c.runCLI(ctx, "grok",
			"-p", req.Prompt,
			"--output-format", "json",
			"--json-schema", strings.TrimSpace(grokGuideSchema),
			"--disable-web-search",
			"--permission-mode", "dontAsk",
			"--max-turns", "1",
			"--no-plan",
		)
	case BackendMLX:
		return c.runMLX(ctx, req)
	default:
		return "", fmt.Errorf("unknown backend %q", req.Backend)
	}
}

func (c *Completer) runCLI(ctx context.Context, name string, args ...string) (string, error) {
	if c.Exec == nil {
		c.Exec = xexec.Real{}
	}
	out, err := c.Exec.Run(ctx, "", name, args...)
	if err != nil {
		var xe *xexec.Error
		if errors.As(err, &xe) && strings.TrimSpace(xe.Stderr) != "" {
			return "", fmt.Errorf("%s: %s", name, strings.TrimSpace(xe.Stderr))
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return unwrapModelOutput(name, strings.TrimSpace(out)), nil
}

func unwrapModelOutput(name, raw string) string {
	if name != "grok" || raw == "" || raw[0] != '{' {
		return raw
	}
	var env struct {
		Text             string          `json:"text"`
		StructuredOutput json.RawMessage `json:"structuredOutput"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return raw
	}
	if len(env.StructuredOutput) > 0 && string(env.StructuredOutput) != "null" {
		return string(env.StructuredOutput)
	}
	if strings.TrimSpace(env.Text) != "" {
		return env.Text
	}
	return raw
}

func (c *Completer) runMLX(ctx context.Context, req Request) (string, error) {
	endpoint, err := chatCompletionsURL(req.MLXURL)
	if err != nil {
		return "", err
	}
	model := strings.TrimSpace(req.MLXModel)
	if model == "" {
		return "", fmt.Errorf("mlx model is empty")
	}
	payload, err := json.Marshal(map[string]any{
		"model":       model,
		"temperature": 0.2,
		"messages": []map[string]string{
			{"role": "user", "content": req.Prompt},
		},
	})
	if err != nil {
		return "", err
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	res, err := httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("mlx %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	text, err := extractChatContent(body)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

func chatCompletionsURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("mlx url is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("mlx url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("mlx url must be http(s)")
	}
	host := strings.ToLower(u.Hostname())
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return "", fmt.Errorf("mlx url must be loopback, got %s", host)
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" || path == "/v1" {
		u.Path = "/v1/chat/completions"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func extractChatContent(raw []byte) (string, error) {
	var parsed struct {
		Choices []struct {
			Text    string `json:"text"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("mlx response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("mlx response: no choices")
	}
	c := parsed.Choices[0]
	if c.Message.Content != "" {
		return c.Message.Content, nil
	}
	if c.Text != "" {
		return c.Text, nil
	}
	return "", fmt.Errorf("mlx response: empty content")
}
