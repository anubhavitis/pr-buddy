import assert from "node:assert/strict";
import test from "node:test";
import { buildPrompt, promptTemplate } from "./prompt";

const sample = {
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
};

test("promptTemplate is the empty format with placeholders", () => {
  const template = promptTemplate();
  assert.match(template, /\{owner\}\/\{repo\}#\{number\}/);
  assert.match(template, /\{title\}/);
  assert.match(template, /\{description\}/);
  assert.match(template, /\{files\}/);
  assert.doesNotMatch(template, /acme|widgets|retry\.go|deadbeef/);
});

test("buildPrompt fills the template and keeps the grouping contract", () => {
  const prompt = buildPrompt(sample);
  assert.match(prompt, /acme\/widgets#12/);
  assert.match(prompt, /Add retry backoff/);
  assert.match(prompt, /Stops the client/);
  assert.match(prompt, /client\/retry.go \+40 -2/);
  assert.match(prompt, /3 to 6 groups/i);
  assert.match(prompt, /contracts \/ types \/ public API/i);
  assert.match(prompt, /"groups"/);
  assert.match(prompt, /"blurb"/);
  assert.doesNotMatch(prompt, /"findings"|severity/i);
  assert.doesNotMatch(prompt, /\{owner\}|\{files\}/);
});
