import assert from "node:assert/strict";
import test from "node:test";
import { buildPrompt } from "./prompt";

test("buildPrompt includes title, files, and understand-first contract", () => {
  const prompt = buildPrompt({
    owner: "acme",
    repo: "widgets",
    number: 12,
    title: "Add retry backoff",
    body: "Stops the client from hammering the API.",
    headSHA: "deadbeef",
    files: [
      { path: "client/retry.go", additions: 40, deletions: 2 },
      { path: "client/retry_test.go", additions: 80, deletions: 0 },
    ],
  });
  assert.match(prompt, /acme\/widgets#12/);
  assert.match(prompt, /Add retry backoff/);
  assert.match(prompt, /Stops the client/);
  assert.match(prompt, /client\/retry.go \+40 -2/);
  assert.match(prompt, /understand first/i);
  assert.match(prompt, /contracts|types|entry/i);
  assert.match(prompt, /"groups"/);
  assert.match(prompt, /"blurb"/);
  assert.doesNotMatch(prompt, /"findings"|severity/i);
});
