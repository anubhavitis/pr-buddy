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

  return `You are ordering a pull request for a human reviewer on GitHub.

PR: ${pr.owner}/${pr.repo}#${pr.number}
Title: ${pr.title}
Description:
${body}

Changed files:
${files}

Produce a reading guide. Order files by what a reviewer must understand first,
not by path. Sequence: contracts / types / entrypoints → implementation → wiring → tests.
Put danger (auth, data, money, concurrency) in the per-file blurb, not the sort key.

Each file gets a 2–3 line blurb: what this file is in this PR.
Each group gets a short summary: why this cluster comes here.

Respond with a single JSON object and nothing else:

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
}
