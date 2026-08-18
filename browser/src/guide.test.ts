import assert from "node:assert/strict";
import test from "node:test";
import { cacheKey, flattenGuide, parseGuide } from "./guide";

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

test("cacheKey is repo + pr + head + backend", () => {
  assert.equal(
    cacheKey({ owner: "acme", repo: "widgets", number: 4, headSHA: "abc", backend: "claude" }),
    "acme/widgets#4:abc:claude",
  );
});
