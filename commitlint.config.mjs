// Commitlint configuration — enforced in CI on every pull request (each
// commit and the PR title, since squash merges use the title as the subject).
// The zero-dependency local mirror of these rules lives in
// .githooks/commit-msg (enable with `make hooks`).
export default {
  extends: ["@commitlint/config-conventional"],
  rules: {
    // Keep in sync with .githooks/commit-msg.
    "header-max-length": [2, "always", 100],
  },
};
