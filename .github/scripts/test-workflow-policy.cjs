const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { test } = require("node:test");
const { spawnSync } = require("node:child_process");

const workflows = path.join(__dirname, "..", "workflows");
const dependabot = fs.readFileSync(
  path.join(workflows, "dependabot-auto-merge.yml"),
  "utf8",
);
const updater = fs.readFileSync(
  path.join(workflows, "update-go-toolchain.yml"),
  "utf8",
);

function step(workflow, name) {
  const block = workflow.split(`      - name: ${name}\n`)[1];
  assert.ok(block, `Missing step: ${name}`);
  return block.split(/\n      - /)[0];
}

function script(block) {
  return block
    .split(/(?:script|run): \|\n/)[1]
    .split("\n")
    .filter((line) => line.startsWith("          "))
    .map((line) => line.slice(10))
    .join("\n");
}

const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
const policy = new AsyncFunction(
  "github",
  "context",
  "core",
  script(step(dependabot, "Require review for Go version metadata changes")),
);

async function evaluate(base, head, autoMerge = false, currentHead = "head") {
  const outputs = {};
  const mutations = [];
  const github = {
    rest: {
      pulls: {
        get: async () => ({
          data: {
            state: "open",
            base: { sha: "base" },
            head: { sha: currentHead },
          },
        }),
      },
      repos: {
        getContent: async ({ ref }) => ({
          data: {
            type: "file",
            encoding: "base64",
            content: Buffer.from(ref === "base" ? base : head).toString(
              "base64",
            ),
          },
        }),
      },
    },
    graphql: async (query) => {
      if (query.includes("mutation")) {
        mutations.push(query);
        return {};
      }
      return {
        repository: {
          pullRequest: {
            id: "pr-id",
            autoMergeRequest: autoMerge ? { enabledAt: "now" } : null,
          },
        },
      };
    },
  };
  await policy(
    github,
    {
      repo: { owner: "example", repo: "test" },
      payload: {
        pull_request: {
          number: 1,
          base: { sha: "base" },
          head: { sha: "head" },
        },
      },
    },
    {
      setOutput: (key, value) => (outputs[key] = value),
      notice: () => {},
    },
  );
  return { outputs, mutations };
}

test("ordinary dependency changes remain eligible", async () => {
  const result = await evaluate(
    "module test\n\ngo 1.27.0\nrequire example.com/lib v1.0.0\n",
    "module test\r\n\r\ngo 1.27.0 // unchanged\r\nrequire example.com/lib v1.0.1\r\n",
  );
  assert.equal(result.outputs.unchanged, "true");
  assert.equal(result.mutations.length, 0);
});

test("Go changes and toolchain additions block and revoke auto-merge", async () => {
  for (const head of [
    "go 1.27.1\n",
    "go 1.28.0\n",
    "go 1.27.0\ntoolchain go1.28.0\n",
  ]) {
    const result = await evaluate("go 1.27.0\n", head, true);
    assert.equal(result.outputs.unchanged, "false");
    assert.equal(result.mutations.length, 1);
  }
});

test("version changes without existing auto-merge do not mutate PRs", async () => {
  const result = await evaluate("go 1.27.0\n", "go 1.28.0\n");
  assert.equal(result.outputs.unchanged, "false");
  assert.equal(result.mutations.length, 0);
});

test("invalid metadata fails closed", async () => {
  await assert.rejects(evaluate("go 1.27.0\n", "module test\n"), /invalid Go/);
});

test("stale events cannot re-enable auto-merge on a newer commit", async () => {
  const result = await evaluate(
    "go 1.27.0\n",
    "go 1.27.0\n",
    false,
    "newer-go-update",
  );
  assert.equal(result.outputs.unchanged, "false");
  assert.equal(result.mutations.length, 0);
  assert.match(
    step(dependabot, "Approve PR"),
    /commit_id: context\.payload\.pull_request\.head\.sha/,
  );
  assert.match(
    step(dependabot, "Enable auto-merge"),
    /--match-head-commit "\$HEAD_SHA"/,
  );
});

test("both Dependabot write steps require the version guard", () => {
  for (const name of ["Approve PR", "Enable auto-merge"]) {
    assert.match(
      step(dependabot, name),
      /steps\.go-policy\.outputs\.unchanged == 'true' &&/,
    );
  }
  assert.doesNotMatch(dependabot, /actions\/checkout/);
});

test("dry runs cannot access publishing steps", () => {
  assert.match(updater, /dry_run:[\s\S]*?default: true/);
  for (const name of [
    "Require PR token",
    "Disable existing auto-merge before a feature update",
    "Create pull request",
    "Enable auto-merge for patch updates",
  ]) {
    assert.match(
      step(updater, name),
      /steps\.mode\.outputs\.publish == 'true'/,
    );
  }
  assert.doesNotMatch(updater, /ref: \$\{\{ github\.event\.repository/);
});

test("publishing requires main; dry runs may use feature branches", () => {
  const run = script(step(updater, "Select run mode"));
  for (const [publish, ref, status] of [
    ["true", "refs/heads/main", 0],
    ["false", "refs/heads/feature", 0],
    ["true", "refs/heads/feature", 1],
  ]) {
    const result = spawnSync("bash", ["-c", run], {
      encoding: "utf8",
      env: {
        ...process.env,
        PUBLISH: publish,
        GITHUB_REF: ref,
        DEFAULT_REF: "refs/heads/main",
        GITHUB_OUTPUT: "/dev/null",
      },
    });
    assert.equal(result.status, status, result.stderr || String(result.error));
  }
});
