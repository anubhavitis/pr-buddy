import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { cacheKey, flattenGuide, parseGuide } from "./guide";

test("parseGuide accepts a document valid against the host guide schema", () => {
  const schema = JSON.parse(
    readFileSync(join(__dirname, "..", "..", "internal", "host", "guide.schema.json"), "utf8"),
  ) as { required?: string[]; properties?: { groups?: unknown } };
  assert.deepEqual(schema.required, ["groups"]);
  assert.ok(schema.properties?.groups);
  const guide = parseGuide(`{
    "groups": [
      {
        "name": "Contracts",
        "summary": "Public API first",
        "files": [{ "path": "api/server.go", "blurb": "HTTP entry for retries." }]
      }
    ]
  }`);
  assert.equal(guide.groups[0].files[0].path, "api/server.go");
});

test("parseGuide reads clean JSON", () => {
  const guide = parseGuide(`{
    "groups": [
      {
        "name": "Contracts",
        "summary": "Public API first",
        "files": [
          { "path": "api/server.go", "blurb": "HTTP entry for retries." }
        ]
      }
    ]
  }`);
  assert.equal(guide.groups[0].name, "Contracts");
  assert.equal(guide.groups[0].files[0].path, "api/server.go");
});

test("parseGuide strips fences and surrounding prose", () => {
  const guide = parseGuide(`Sure.

\`\`\`json
{"groups":[{"name":"A","summary":"s","files":[{"path":"a.go","blurb":"b"}]}]}
\`\`\`

hope this helps`);
  assert.equal(guide.groups[0].files[0].path, "a.go");
});

test("parseGuide rejects missing groups", () => {
  assert.throws(() => parseGuide(`{"summary":"nope"}`));
  assert.throws(() => parseGuide(`not json`));
});

test("flattenGuide keeps first mention and appends leftovers", () => {
  const guide = parseGuide(`{
    "groups": [
      {
        "name": "Core",
        "summary": "start",
        "files": [
          { "path": "b.go", "blurb": "impl" },
          { "path": "a.go", "blurb": "types" }
        ]
      },
      {
        "name": "Tests",
        "summary": "end",
        "files": [
          { "path": "b.go", "blurb": "ignored duplicate" },
          { "path": "b_test.go", "blurb": "coverage" }
        ]
      }
    ]
  }`);
  const rows = flattenGuide(guide, ["z.go", "a.go", "b.go", "b_test.go"]);
  assert.deepEqual(
    rows.map((r) => r.path),
    ["b.go", "a.go", "b_test.go", "z.go"],
  );
  assert.equal(rows[0].group, "Core");
  assert.equal(rows[0].blurb, "impl");
  assert.equal(rows[3].group, "Other");
  assert.equal(rows[3].blurb, "");
});

test("flattenGuide drops paths the PR does not contain", () => {
  const guide = parseGuide(
    `{"groups":[{"name":"X","summary":"s","files":[{"path":"ghost.go","blurb":"no"},{"path":"real.go","blurb":"yes"}]}]}`,
  );
  const rows = flattenGuide(guide, ["real.go"]);
  assert.deepEqual(
    rows.map((r) => r.path),
    ["real.go"],
  );
});

test("cacheKey changes when the file set grows", () => {
  const base = {
    owner: "acme",
    repo: "widgets",
    number: 4,
    headSHA: "abc",
    backend: "claude",
  };
  const first = cacheKey({ ...base, files: ["a.go"] });
  const later = cacheKey({ ...base, files: ["a.go", "b.go"] });
  assert.notEqual(first, later);
});

test("cacheKey ignores file order and duplicates", () => {
  const base = {
    owner: "acme",
    repo: "widgets",
    number: 4,
    headSHA: "abc",
    backend: "claude",
  };
  assert.equal(
    cacheKey({ ...base, files: ["b.go", "a.go", "b.go"] }),
    cacheKey({ ...base, files: ["a.go", "b.go"] }),
  );
});

test("cacheKey changes when backend, model, or head changes", () => {
  const files = ["a.go"];
  const a = cacheKey({
    owner: "acme",
    repo: "widgets",
    number: 4,
    headSHA: "abc",
    backend: "claude",
    files,
  });
  const b = cacheKey({
    owner: "acme",
    repo: "widgets",
    number: 4,
    headSHA: "abc",
    backend: "grok",
    files,
  });
  const c = cacheKey({
    owner: "acme",
    repo: "widgets",
    number: 4,
    headSHA: "def",
    backend: "claude",
    files,
  });
  const d = cacheKey({
    owner: "acme",
    repo: "widgets",
    number: 4,
    headSHA: "abc",
    backend: "claude",
    model: "opus",
    files,
  });
  assert.notEqual(a, b);
  assert.notEqual(a, c);
  assert.notEqual(a, d);
});
