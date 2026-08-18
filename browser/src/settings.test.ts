import assert from "node:assert/strict";
import test from "node:test";
import { defaultSettings, normalizeSettings } from "./settings";

test("normalizeSettings fills defaults and rejects unknown backends", () => {
  assert.deepEqual(normalizeSettings(undefined), defaultSettings);
  assert.equal(normalizeSettings({ backend: "nope" as never }).backend, "claude");
  assert.equal(normalizeSettings({ backend: "mlx", mlxModel: " qwen " }).mlxModel, "qwen");
  assert.equal(normalizeSettings({ hostUrl: "http://127.0.0.1:17342/" }).hostUrl, "http://127.0.0.1:17342");
});
