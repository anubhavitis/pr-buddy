export type PromptFile = {
  path: string;
  additions?: number;
  deletions?: number;
};

export type PromptPR = {
  owner: string;
  repo: string;
  number: number;
  title: string;
  body: string;
  headSHA: string;
  files: PromptFile[];
};

export const PROMPT_TEMPLATE = `You are writing a reading order for a human reviewing a GitHub pull request.

Do not review the change. Do not list bugs. Order the files so the reviewer
understands the shape of the change before they read the wiring.

How to group
- 3 to 6 groups. Short names, two to four words (for example: Contracts, Then the host, Wiring).
- Read order: contracts / types / public API first, then implementation, then wiring, then tests.
- A group is files that should be read together, not a folder name.
- Put danger (auth, data, money, concurrency) in the file blurb, not in the sort key.

What to write
- Group summary: one line on why this cluster comes here.
- File blurb: two or three lines on what this file is doing in this PR.

This pull request
PR: {owner}/{repo}#{number}
Title: {title}
Description:
{description}

Changed files:
{files}

Reply with one JSON object and nothing else:

{
  "groups": [
    {
      "name": "group name",
      "summary": "why this group, what to look for",
      "files": [
        { "path": "path/relative/to/repo.go", "blurb": "two or three lines on what this file is" }
      ]
    }
  ]
}

Use only the paths listed above. Do not invent files. Do not report defects.`;

export function promptTemplate(): string {
  return PROMPT_TEMPLATE;
}

export function buildPrompt(pr: PromptPR): string {
  const files = pr.files
    .map((f) => {
      const plus = f.additions != null ? `+${f.additions}` : "";
      const minus = f.deletions != null ? `-${f.deletions}` : "";
      const stats = [plus, minus].filter(Boolean).join(" ");
      return stats ? `- ${f.path} ${stats}` : `- ${f.path}`;
    })
    .join("\n");

  const body = pr.body.trim() || "(no description)";

  return PROMPT_TEMPLATE.replaceAll("{owner}", pr.owner)
    .replaceAll("{repo}", pr.repo)
    .replaceAll("{number}", String(pr.number))
    .replaceAll("{title}", pr.title)
    .replaceAll("{description}", body)
    .replaceAll("{files}", files);
}
