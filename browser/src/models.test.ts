import assert from "node:assert/strict";
import test from "node:test";
import { modelsForBackend, modelsPath } from "./models";

test("modelsForBackend falls back to claude aliases", () => {
  assert.deepEqual(
    modelsForBackend("claude", []).map((m) => m.id),
    ["sonnet", "opus", "haiku", "fable"],
  );
  assert.deepEqual(modelsForBackend("grok", []), []);
  assert.deepEqual(
    modelsForBackend("claude", [{ id: "opus" }]).map((m) => m.id),
    ["opus"],
  );
});

test("modelsPath is a host GET with backend", () => {
  assert.equal(
    modelsPath("http://127.0.0.1:17342", "claude"),
    "http://127.0.0.1:17342/models?backend=claude",
  );
  assert.match(modelsPath("http://127.0.0.1:17342/", "mlx", "http://127.0.0.1:8080/v1"), /mlx_url=/);
});
