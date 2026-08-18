import assert from "node:assert/strict";
import test from "node:test";
import { completeRequest } from "./host";
import { defaultSettings } from "./settings";

test("completeRequest uses the host JSON field names", () => {
  const body = completeRequest(
    { ...defaultSettings, backend: "mlx", mlxUrl: "http://127.0.0.1:8080/v1", mlxModel: "qwen" },
    "order these files",
  );
  assert.deepEqual(Object.keys(body).sort(), ["backend", "mlx_model", "mlx_url", "prompt"]);
  assert.equal(body.backend, "mlx");
  assert.equal(body.prompt, "order these files");
  assert.equal(body.mlx_url, "http://127.0.0.1:8080/v1");
  assert.equal(body.mlx_model, "qwen");
});
