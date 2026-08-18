import assert from "node:assert/strict";
import test from "node:test";
import { flattenGuide, parseGuide } from "./guide";
import {
  fileSetSignature,
  groupRows,
  isCommitsPickerLabel,
  shouldWalkUpForCommitsDock,
  isFileFilterField,
  isMergeStatusLabel,
  isOurMutationTarget,
  isToolbarCompanionLabel,
  orderedPaths,
  pathsFromTreeItems,
  pathsToTree,
  shortStatus,
  shouldRedockPanel,
} from "./overlay";

test("groupRows keeps adjacent files in the same section", () => {
  const guide = parseGuide(`{
    "groups": [
      { "name": "API", "summary": "enter here", "files": [
        { "path": "a.go", "blurb": "handler" },
        { "path": "b.go", "blurb": "types" }
      ]},
      { "name": "Tests", "summary": "last", "files": [
        { "path": "a_test.go", "blurb": "cases" }
      ]}
    ]
  }`);
  const rows = flattenGuide(guide, ["a.go", "b.go", "a_test.go", "orphan.go"]);
  const groups = groupRows(rows);
  assert.deepEqual(
    groups.map((g) => [g.name, g.files.map((f) => f.path)]),
    [
      ["API", ["a.go", "b.go"]],
      ["Tests", ["a_test.go"]],
      ["Other", ["orphan.go"]],
    ],
  );
});

test("isMergeStatusLabel matches the GitHub merge pill, not body text", () => {
  assert.equal(isMergeStatusLabel("Ready to merge"), true);
  assert.equal(isMergeStatusLabel("  Ready to merge  "), true);
  assert.equal(isMergeStatusLabel("Review required"), true);
  assert.equal(isMergeStatusLabel("This branch has conflicts"), true);
  assert.equal(isMergeStatusLabel("Please review and then it will be ready to merge later"), false);
  assert.equal(isMergeStatusLabel("Submit review"), false);
});

test("isToolbarCompanionLabel is only the Code/Preview pills", () => {
  assert.equal(isToolbarCompanionLabel("Code"), true);
  assert.equal(isToolbarCompanionLabel("Preview"), true);
  assert.equal(isToolbarCompanionLabel("Submit review"), false);
});

test("isCommitsPickerLabel matches the Files commit filter", () => {
  assert.equal(isCommitsPickerLabel("All commits"), true);
  assert.equal(isCommitsPickerLabel("  All commits  "), true);
  assert.equal(isCommitsPickerLabel("3 commits"), true);
  assert.equal(isCommitsPickerLabel("Ready to merge"), false);
  assert.equal(isCommitsPickerLabel("Filter files..."), false);
});

test("shouldWalkUpForCommitsDock stops at a parent that also owns the file filter", () => {
  assert.equal(shouldWalkUpForCommitsDock({ childCount: 1, hasFileFilter: false, hasFileTree: false }), true);
  assert.equal(shouldWalkUpForCommitsDock({ childCount: 3, hasFileFilter: false, hasFileTree: false }), false);
  assert.equal(shouldWalkUpForCommitsDock({ childCount: 1, hasFileFilter: true, hasFileTree: false }), false);
  assert.equal(shouldWalkUpForCommitsDock({ childCount: 4, hasFileFilter: true, hasFileTree: false }), false);
  assert.equal(shouldWalkUpForCommitsDock({ childCount: 2, hasFileFilter: false, hasFileTree: true }), false);
});

test("shortStatus hides raw CLI failures", () => {
  assert.equal(
    shortStatus("claude -p --bare --output-format text: exit status 1: not logged in"),
    "claude failed",
  );
  assert.equal(shortStatus("host offline — start pr-buddy-host"), "host offline");
  assert.equal(shortStatus("10 files · cached"), "10 files · cached");
});

test("fileSetSignature ignores order so a reorder does not retrigger", () => {
  assert.equal(
    fileSetSignature(["b.ts", "a.ts", "b.ts"]),
    fileSetSignature(["a.ts", "b.ts"]),
  );
});

test("shouldRedockPanel only when the pill left the toolbar row", () => {
  const toolbar = { id: "toolbar" };
  const other = { id: "other" };
  assert.equal(shouldRedockPanel(toolbar, toolbar), false);
  assert.equal(shouldRedockPanel(other, toolbar), true);
  assert.equal(shouldRedockPanel(null, toolbar), true);
});

test("isOurMutationTarget ignores our own remounts", () => {
  assert.equal(isOurMutationTarget("pr-buddy-panel"), true);
  assert.equal(isOurMutationTarget("pr-buddy-tree"), true);
  assert.equal(isOurMutationTarget("pr-buddy-status"), true);
  assert.equal(isOurMutationTarget("pr-buddy-dialog"), true);
  assert.equal(isOurMutationTarget("files"), false);
});

test("orderedPaths is a flat review list with no copy", () => {
  const guide = parseGuide(`{
    "groups": [
      { "name": "API", "summary": "enter here", "files": [
        { "path": "b.go", "blurb": "types" },
        { "path": "a.go", "blurb": "handler" }
      ]}
    ]
  }`);
  const rows = flattenGuide(guide, ["a.go", "b.go", "z.go"]);
  assert.deepEqual(orderedPaths(rows), ["b.go", "a.go", "z.go"]);
});

test("pathsToTree nests by folder and keeps review numbers", () => {
  const tree = pathsToTree([
    "apps/web/lib/outcome/a.ts",
    "apps/web/lib/routing/b.ts",
    "apps/web/app/[locale]/(chrome)/[category]/layout.tsx",
    "apps/web/lib/routing/c.ts",
  ]);
  assert.deepEqual(tree, [
    {
      kind: "dir",
      name: "apps/web",
      children: [
        {
          kind: "dir",
          name: "lib",
          children: [
            {
              kind: "dir",
              name: "outcome",
              children: [{ kind: "file", name: "a.ts", path: "apps/web/lib/outcome/a.ts", index: 1 }],
            },
            {
              kind: "dir",
              name: "routing",
              children: [
                { kind: "file", name: "b.ts", path: "apps/web/lib/routing/b.ts", index: 2 },
                { kind: "file", name: "c.ts", path: "apps/web/lib/routing/c.ts", index: 4 },
              ],
            },
          ],
        },
        {
          kind: "dir",
          name: "app/[locale]/(chrome)/[category]",
          children: [
            {
              kind: "file",
              name: "layout.tsx",
              path: "apps/web/app/[locale]/(chrome)/[category]/layout.tsx",
              index: 3,
            },
          ],
        },
      ],
    },
  ]);
});

test("isFileFilterField matches old and new Files panes", () => {
  assert.equal(isFileFilterField("Filter files...", ""), true);
  assert.equal(isFileFilterField("", "Filter files"), true);
  assert.equal(isFileFilterField("Filter changed files", ""), true);
  assert.equal(isFileFilterField("Search issues", ""), false);
});

test("pathsFromTreeItems rebuilds full paths from aria levels", () => {
  assert.deepEqual(
    pathsFromTreeItems([
      { level: 1, name: "apps/web", kind: "directory" },
      { level: 2, name: "app/[locale]/(chrome)", kind: "directory" },
      { level: 3, name: "[category]", kind: "directory" },
      { level: 4, name: "layout.tsx", kind: "file" },
      { level: 4, name: "page.tsx", kind: "file" },
      { level: 3, name: "event/[slug]", kind: "directory" },
      { level: 4, name: "page.test.tsx", kind: "file" },
      { level: 2, name: "lib/routing/bare-id-path.ts", kind: "file" },
    ]),
    [
      "apps/web/app/[locale]/(chrome)/[category]/layout.tsx",
      "apps/web/app/[locale]/(chrome)/[category]/page.tsx",
      "apps/web/app/[locale]/(chrome)/event/[slug]/page.test.tsx",
      "lib/routing/bare-id-path.ts",
    ],
  );
});
