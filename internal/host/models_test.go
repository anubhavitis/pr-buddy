package host

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xexec "github.com/anubhavitis/pr-buddy/internal/exec"
)

func TestParseGrokModelsReadsDefaultAndIDs(t *testing.T) {
	got := parseGrokModels(`You are logged in with grok.com.

Default model: grok-4.6

Available models:
  * grok-4.6 (default)
  - grok-4.5
`)
	if got.Default != "grok-4.6" {
		t.Fatalf("default %q", got.Default)
	}
	if len(got.Models) != 2 || got.Models[0].ID != "grok-4.6" || got.Models[1].ID != "grok-4.5" {
		t.Fatalf("models %+v", got.Models)
	}
}

func TestListModelsClaudeIsTheAliasSet(t *testing.T) {
	fake := xexec.NewFake()
	c := &Completer{Exec: fake}
	list, err := c.ListModels(context.Background(), BackendClaude, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, m := range list.Models {
		ids[m.ID] = true
	}
	for _, want := range []string{"sonnet", "opus", "haiku", "fable"} {
		if !ids[want] {
			t.Fatalf("missing %s in %+v", want, list.Models)
		}
	}
	if len(fake.CommandLines()) != 0 {
		t.Fatal("claude list must not shell out")
	}
}

func TestListModelsGrokRunsGrokModels(t *testing.T) {
	fake := xexec.NewFake().RespondOK("grok models", "Default model: grok-4.6\nAvailable models:\n  * grok-4.6 (default)\n")
	c := &Completer{Exec: fake}
	list, err := c.ListModels(context.Background(), BackendGrok, "")
	if err != nil {
		t.Fatal(err)
	}
	if list.Default != "grok-4.6" || len(list.Models) != 1 {
		t.Fatalf("%+v", list)
	}
	if !strings.Contains(fake.CommandLines()[0], "grok models") {
		t.Fatalf("command %q", fake.CommandLines()[0])
	}
}

func TestHandlerModels(t *testing.T) {
	srv := httptest.NewServer(Handler(&Completer{Exec: xexec.NewFake()}))
	defer srv.Close()
	res, err := http.Get(srv.URL + "/models?backend=claude")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("status %d %s", res.StatusCode, body)
	}
	var out modelsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Backend != "claude" || len(out.Models) == 0 {
		t.Fatalf("%+v", out)
	}
}
