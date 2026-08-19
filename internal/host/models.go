package host

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

type ModelInfo struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type ModelList struct {
	Backend Backend     `json:"backend"`
	Default string      `json:"default,omitempty"`
	Models  []ModelInfo `json:"models"`
}

var grokModelLine = regexp.MustCompile(`(?m)^\s*[\*\-]\s+(\S+)`)

func (c *Completer) ListModels(ctx context.Context, backend Backend, mlxURL string) (ModelList, error) {
	if err := ctx.Err(); err != nil {
		return ModelList{}, err
	}
	switch backend {
	case BackendClaude:
		return ModelList{
			Backend: BackendClaude,
			Models: []ModelInfo{
				{ID: "sonnet"},
				{ID: "opus"},
				{ID: "haiku"},
				{ID: "fable"},
			},
		}, nil
	case BackendGrok:
		return c.listGrok(ctx)
	case BackendMLX:
		return c.listMLX(ctx, mlxURL)
	default:
		return ModelList{}, fmt.Errorf("unknown backend %q", backend)
	}
}

func (c *Completer) listGrok(ctx context.Context) (ModelList, error) {
	if c.Exec == nil {
		c.Exec = xexec.Real{}
	}
	out, err := c.Exec.Run(ctx, "", "grok", "models")
	if err != nil {
		return ModelList{}, fmt.Errorf("grok: %w", err)
	}
	list := parseGrokModels(out)
	list.Backend = BackendGrok
	return list, nil
}

func parseGrokModels(raw string) ModelList {
	list := ModelList{Models: nil}
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(t, "Default model:"); ok {
			list.Default = strings.TrimSpace(rest)
		}
	}
	for _, m := range grokModelLine.FindAllStringSubmatch(raw, -1) {
		id := strings.TrimRight(m[1], ",")
		if id == "" {
			continue
		}
		list.Models = append(list.Models, ModelInfo{ID: id})
	}
	return list
}

func (c *Completer) listMLX(ctx context.Context, mlxURL string) (ModelList, error) {
	list := ModelList{Backend: BackendMLX}
	if strings.TrimSpace(mlxURL) == "" {
		return list, nil
	}
	endpoint, err := modelsURL(mlxURL)
	if err != nil {
		return ModelList{}, err
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ModelList{}, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return ModelList{}, fmt.Errorf("mlx: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return ModelList{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return list, nil
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return list, nil
	}
	for _, m := range parsed.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		list.Models = append(list.Models, ModelInfo{ID: id})
	}
	return list, nil
}

func modelsURL(raw string) (string, error) {
	endpoint, err := chatCompletionsURL(raw)
	if err != nil {
		return "", err
	}
	return strings.Replace(endpoint, "/chat/completions", "/models", 1), nil
}
