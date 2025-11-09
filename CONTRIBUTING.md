# Contributing

Thanks for your interest in contributing to pushpop.

This project keeps a simple set of conventions to make collaboration smooth:

- Language for source comments: English
  - All comments and doc strings inside source files should be written in English.
  - Chat and issue discussion may be in any language (your team prefers French), but
    keep the repository source comments consistent in English.

- Formatting
  - Run `gofmt -w .` before committing changes. This repo follows Go formatting rules.

- Tests
  - Add or update unit tests for any non-trivial behavior. Run `go test ./...` and ensure
    tests pass locally before opening a pull request.

- Commits
  - Use clear, small commits with imperative messages, e.g. `feat: add HTTP server` or
    `fix: handle error when ...`.

- Code review
  - Open a pull request for non-trivial changes and include a short description of the
    intent and any manual test steps.

Notes
- We intentionally do not add repository-level hooks. Developers can enable local
  hooks if they wish, but they are not enforced by the repo.

Optional local workflow tips
- Install tools locally to keep your workflow smooth:
  - `go install golang.org/x/tools/cmd/goimports@latest` (optional to manage imports)
  - `gofmt -w .` to format
  - `go test ./...` to run tests

Thank you for contributing!
