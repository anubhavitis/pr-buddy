import assert from "node:assert/strict";
import test from "node:test";


import { defaultSettings, modelLabel, normalizeSettings, selectedModel } from "./settings";

test("normalizeSettings fills defaults and rejects unknown backends", () => {
  assert.deepEqual(normalizeSettings(undefined), defaultSettings);
  assert.equal(normalizeSettings({ backend: "nope" as never }).backend, "claude");
  assert.equal(normalizeSettings({ backend: "mlx", mlxModel: " qwen " }).mlxModel, "qwen");
  assert.equal(normalizeSettings({ claudeModel: " opus " }).claudeModel, "opus");
  assert.equal(normalizeSettings({ grokModel: " grok-4.6 " }).grokModel, "grok-4.6");
  assert.equal(normalizeSettings({ hostUrl: "http://127.0.0.1:17342/" }).hostUrl, "http://127.0.0.1:17342");
});

test("normalizeSettings drops missing and bogus model ids", () => {
  assert.equal(normalizeSettings({}).claudeModel, "");
  assert.equal(normalizeSettings({ claudeModel: "undefined" }).claudeModel, "");
  assert.equal(normalizeSettings({ grokModel: "null" }).grokModel, "");
  assert.equal(normalizeSettings({ mlxModel: undefined }).mlxModel, "");
});

test("selectedModel follows the active backend", () => {
  const s = normalizeSettings({
    backend: "claude",
    claudeModel: "opus",
    grokModel: "grok-4.6",
    mlxModel: "qwen",
  });
  assert.equal(selectedModel(s), "opus");
  assert.equal(selectedModel({ ...s, backend: "grok" }), "grok-4.6");
  assert.equal(selectedModel({ ...s, backend: "mlx" }), "qwen");
  assert.equal(modelLabel({ ...s, backend: "claude", claudeModel: "" }), "claude");
});
