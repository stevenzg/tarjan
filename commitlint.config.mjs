// Commitlint configuration — enforced in CI on every pull request (each
// commit and the PR title, since squash merges use the title as the subject).
// The zero-dependency local mirror of these rules lives in
// .githooks/commit-msg (enable with `make hooks`).
export default {
  extends: ["@commitlint/config-conventional"],
  // Dependabot's generated messages ("build(deps): Bump …") use a capitalized
  // subject and long metadata lines in the body, neither of which can be
  // changed via dependabot.yml — exempt them instead of failing every update
  // PR. The type/scope prefix itself is still set in .github/dependabot.yml.
  ignores: [(message) => /^(build|ci)\(deps[^)]*\): bump /i.test(message)],
  rules: {
    // Keep in sync with .githooks/commit-msg.
    "header-max-length": [2, "always", 100],
  },
};
