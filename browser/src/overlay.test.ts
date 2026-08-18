import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { flattenGuide, parseGuide } from "./guide";
import {
  commitsDockAction,
  fileSetSignature,
  groupRows,
  isCommitsPickerLabel,
  isFileFilterField,
  isFilesToolbarRow,
  isMergeStatusLabel,
  isOurMutationTarget,
  isToolbarCompanionLabel,
  orderedPaths,
  pathsFromTreeItems,
  pathForDiffFragment,
  pathsToTree,
  sha256Hex,
  shortStatus,
  shouldRedockPanel,
  currentNotePath,
  diffAnchor,
  fileNoteCopy,
  isDiffRegionId,
  pathFromVisibleLabel,
  pickNoteInsert,
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

test("isFilesToolbarRow is GitHub's horizontal files toolbar, not the All-commits cell", () => {
  assert.equal(
    isFilesToolbarRow({ component: "Stack", direction: "horizontal", hasTreeToggle: true }),
    true,
  );
  assert.equal(
    isFilesToolbarRow({ component: "Stack", direction: "vertical", hasTreeToggle: true }),
    false,
  );
  assert.equal(
    isFilesToolbarRow({ component: "Stack", direction: "horizontal", hasTreeToggle: false }),
    false,
  );
  assert.equal(isFilesToolbarRow({ component: null, direction: "horizontal", hasTreeToggle: true }), false);
});

test("commitsDockAction walks the All-commits cell and inserts on the toolbar row", () => {
  // hide-when-stuck-large cell: d-none + All commits button. Not the toolbar.
  assert.equal(
    commitsDockAction({ isFilesToolbarRow: false, hasFileFilter: false, hasFileTree: false }),
    "walk",
  );
  // Outer Stack: tree toggle | Open | All commits | title. Insert as a peer of the cell.
  assert.equal(
    commitsDockAction({ isFilesToolbarRow: true, hasFileFilter: false, hasFileTree: false }),
    "insert",
  );
  assert.equal(
    commitsDockAction({ isFilesToolbarRow: false, hasFileFilter: true, hasFileTree: false }),
    "abort",
  );
  assert.equal(
    commitsDockAction({ isFilesToolbarRow: true, hasFileFilter: false, hasFileTree: true }),
    "abort",
  );
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

test("toolbar panel clears the fixed fallback offset so it stays in the dock", () => {
  const css = readFileSync(join(__dirname, "styles.css"), "utf8");
  const block = css.match(/#pr-buddy-panel\.pr-buddy-panel--toolbar\s*\{[^}]+\}/)?.[0] ?? "";
  assert.match(block, /top:\s*auto/);
  assert.match(block, /right:\s*auto/);
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

test("diffAnchor is GitHub's #diff- plus sha256 of the path", () => {
  const path = "apps/web/app/[locale]/(chrome)/[category]/layout.tsx";
  assert.equal(sha256Hex(""), "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");
  assert.equal(sha256Hex("abc"), "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
  assert.equal(sha256Hex(path), "0f52acc01edfb4f29f5e6e18ad88dda053b8a236a97ac1851c3e7b4f8696e6e4");
  assert.equal(diffAnchor(path), "#diff-0f52acc01edfb4f29f5e6e18ad88dda053b8a236a97ac1851c3e7b4f8696e6e4");
  assert.equal(
    pathForDiffFragment(
      ["other.ts", path],
      "#diff-0f52acc01edfb4f29f5e6e18ad88dda053b8a236a97ac1851c3e7b4f8696e6e4",
    ),
    path,
  );
});

test("file notes resolve the current /changes file and fall back to group summary", () => {
  const path = "apps/web/app/[locale]/(chrome)/[category]/layout.tsx";
  assert.equal(fileNoteCopy({ blurb: "entry", groupSummary: "types" }), "entry");
  assert.equal(fileNoteCopy({ blurb: "", groupSummary: "read types first" }), "read types first");
  assert.equal(pathFromVisibleLabel("layout.tsx", [path, "apps/web/other.ts"]), path);
  assert.equal(
    currentNotePath({
      paths: ["a.ts", path],
      hash: "#diff-0f52acc01edfb4f29f5e6e18ad88dda053b8a236a97ac1851c3e7b4f8696e6e4",
      activePath: "a.ts",
    }),
    path,
  );
  assert.equal(isDiffRegionId("diff-0f52acc01edfb4f29f5e6e18ad88dda053b8a236a97ac1851c3e7b4f8696e6e4"), true);
  assert.equal(isDiffRegionId("heading-_R_4amal9s5_"), false);
  assert.equal(pickNoteInsert({ hasHeaderWrapper: true, hasDiffTable: true }), "inside-table-wrap");
  assert.equal(pickNoteInsert({ hasHeaderWrapper: false, hasDiffTable: true }), "inside-table-wrap");
  assert.equal(pickNoteInsert({ hasHeaderWrapper: true, hasDiffTable: false }), "after-header-wrapper");
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
