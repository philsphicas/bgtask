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
const linterUpdater = fs.readFileSync(
  path.join(workflows, "update-golangci-lint.yml"),
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

test("dependency remediation runs even when Go is current", () => {
  assert.doesNotMatch(step(updater, "Remediate vulnerable modules"), /if:/);
  for (const name of ["Create pull request", "Test updated module"]) {
    assert.match(
      step(updater, name),
      /steps\.changes\.outputs\.changed == 'true'/,
    );
  }
  assert.match(
    step(updater, "Enable auto-merge for patch updates"),
    /steps\.version\.outputs\.is-patch != 'false'/,
  );
  assert.match(updater, /cron: "0 10 \* \* \*"/);
});

test("linter publishing preserves dry-run, main-only and major-review safeguards", () => {
  assert.match(linterUpdater, /dry_run:[\s\S]*?default: true/);
  assert.equal(
    script(step(linterUpdater, "Select run mode")),
    script(step(updater, "Select run mode")),
  );
  for (const name of [
    "Require PR token",
    "Disable existing auto-merge before a major update",
    "Create pull request",
    "Enable auto-merge for non-major updates",
  ]) {
    assert.match(
      step(linterUpdater, name),
      /steps\.mode\.outputs\.publish == 'true'/,
    );
  }
  assert.match(
    step(linterUpdater, "Enable auto-merge for non-major updates"),
    /steps\.version\.outputs\.major == 'false'/,
  );
  assert.match(
    step(linterUpdater, "Enable auto-merge for non-major updates"),
    /--match-head-commit "\$HEAD_SHA"/,
  );
  assert.match(
    step(linterUpdater, "Create pull request"),
    /add-paths: \.golangci-version/,
  );
});

test("linter updater validates and compares stable versions without downgrading", () => {
  const os = require("node:os");
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "bgtask-linter-update-"));
  try {
    const run = script(step(linterUpdater, "Determine golangci-lint update"));
    for (const [current, latest, expected, major, status] of [
      ["v2.13.2", "v2.15.0", true, false, 0],
      ["v2.9.0", "v2.15.0", true, false, 0],
      ["v2.15.0", "v3.0.0", true, true, 0],
      ["v2.15.0", "v2.15.0", false, false, 0],
      ["v2.15.0", "v2.14.0", false, false, 0],
      ["v3.0.0", "v2.15.0", false, true, 0],
      ["v2.15.0", "v2.16.0-rc1", false, false, 1],
      ["invalid", "v2.15.0", false, false, 1],
    ]) {
      fs.writeFileSync(path.join(dir, ".golangci-version"), `${current}\n`);
      const output = path.join(dir, "outputs");
      fs.writeFileSync(output, "");
      const result = spawnSync(
        "bash",
        ["-c", `gh() { printf '%s\\n' "$LATEST"; }\n${run}`],
        {
          cwd: dir,
          encoding: "utf8",
          env: { ...process.env, LATEST: latest, GITHUB_OUTPUT: output },
        },
      );
      assert.equal(result.status, status, result.stderr);
      assert.equal(
        fs.readFileSync(path.join(dir, ".golangci-version"), "utf8").trim(),
        expected ? latest : current,
      );
      if (status === 0) {
        const outputs = fs.readFileSync(output, "utf8");
        assert.ok(outputs.includes(`update=${expected}\n`));
        assert.ok(outputs.includes(`major=${major}\n`));
      }
    }
    const result = spawnSync("bash", ["-c", `gh() { return 1; }\n${run}`], {
      cwd: dir,
      env: { ...process.env, GITHUB_OUTPUT: path.join(dir, "outputs") },
    });
    assert.notEqual(result.status, 0);
  } finally {
    fs.rmSync(dir, { recursive: true });
  }
});
