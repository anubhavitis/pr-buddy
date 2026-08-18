import assert from "node:assert/strict";
import test from "node:test";
import { isFilesTab, isFilesView, parseFilePaths, parseHeadSHA, parsePRURL, parseTitle } from "./pr";

const fixture = `
<title>Eugene/release verify by ejahnGithub · Pull Request #11000 · cli/cli · GitHub</title>
<h1 class="gh-header-title">
  <bdi class="js-issue-title markdown-title">Eugene/release verify</bdi>
  <span class="f1-light color-fg-muted">#11000</span>
</h1>
<details-menu src="/cli/cli/pull/11000/show_toc?base_sha=17af24e147629aa1aed2546e87e9323aeabf4c8c&amp;sha1=17af24e147629aa1aed2546e87e9323aeabf4c8c&amp;sha2=74c6a36c20cdd14a64599fa6f8e996e1b3b06bf4"></details-menu>
<li data-target="file-tree.fileTreeNode" data-tree-entry-type="file">
  <span data-filterable-item-text hidden>pkg/cmd/attestation/artifact/artifact.go</span>
</li>
<copilot-diff-entry data-file-path="pkg/cmd/release/release.go"></copilot-diff-entry>
<div class="file js-file" data-tagsearch-path="pkg/cmd/attestation/verification/sigstore.go"></div>
`;

test("parsePRURL reads owner repo number from files tab", () => {
  const id = parsePRURL("https://github.com/cli/cli/pull/11000/files");
  assert.deepEqual(id, { owner: "cli", repo: "cli", number: 11000 });
  assert.deepEqual(parsePRURL("https://github.com/cli/cli/pull/11000/changes"), {
    owner: "cli",
    repo: "cli",
    number: 11000,
  });
  assert.equal(parsePRURL("https://github.com/cli/cli/issues/1"), null);
});

test("isFilesTab accepts /files and /changes", () => {
  assert.equal(isFilesTab("https://github.com/cli/cli/pull/11000/files"), true);
  assert.equal(isFilesTab("https://github.com/cli/cli/pull/11000/files?diff=split"), true);
  assert.equal(isFilesTab("https://github.com/cli/cli/pull/11000/changes"), true);
  assert.equal(isFilesTab("https://github.com/cli/cli/pull/11000"), false);
});

test("isFilesView can detect a selected Files tab without /files in the URL", () => {
  const doc = {
    querySelector(sel: string) {
      return sel.includes('href$="/files"') ? {} : null;
    },
  };
  assert.equal(isFilesView("https://github.com/cli/cli/pull/11000", doc), true);
});

test("parseHeadSHA prefers sha2 from the compare toc", () => {
  assert.equal(parseHeadSHA(fixture), "74c6a36c20cdd14a64599fa6f8e996e1b3b06bf4");
});

test("parseTitle uses the issue title", () => {
  assert.equal(parseTitle(fixture), "Eugene/release verify");
});

test("parseFilePaths unions tree and diff attributes, unique, stable", () => {
  assert.deepEqual(parseFilePaths(fixture), [
    "pkg/cmd/attestation/artifact/artifact.go",
    "pkg/cmd/release/release.go",
    "pkg/cmd/attestation/verification/sigstore.go",
  ]);
});
