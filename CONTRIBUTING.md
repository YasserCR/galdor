# Contributing to galdor

Thanks for considering a contribution. galdor is at a very early stage (Phase 0); contribution surface is small but feedback, ADR comments and code review on early PRs are all welcome.

## Code of Conduct

This project adheres to the [Contributor Covenant 2.1](CODE_OF_CONDUCT.md). By participating you agree to uphold it.

## Getting started

Requirements:

- Go **1.25+** (no CGO required; the floor was bumped from 1.22 in ADR-003)
- `golangci-lint` (matched to the version pinned in CI)
- `git`

```bash
git clone https://github.com/YasserCR/galdor.git
cd galdor
go build ./...
go test -race ./...
golangci-lint run
```

## How to contribute

1. **Open an issue first** for anything non-trivial. We'd rather align on direction before you spend hours on a PR.
2. **Keep PRs small and focused.** One concern per PR. Refactors separate from features.
3. **Tests are not optional.** New code in `pkg/` needs unit tests with > 80% coverage of the package. `context.Context` must be accepted on any blocking operation.
4. **Document exported symbols.** GoDoc comments on everything exported.
5. **No new dependencies in `pkg/` core without an ADR.** Adapters can pull their own deps inside their own Go module.
6. **No `panic` outside `init`.** Return errors as values.

## Commit style

- Conventional-ish: `feat(area): …`, `fix(area): …`, `docs: …`, `chore: …`, `refactor: …`.
- Subject line in imperative mood, <= 72 chars.
- Body explains the *why*, not the *what*. The diff explains the *what*.

## Developer Certificate of Origin (DCO)

galdor uses the **DCO** instead of a CLA. By signing off on your commits you certify that you wrote the code or have the right to submit it under this project's Apache 2.0 license. See [`DCO.txt`](DCO.txt) for the full text.

### How to sign off

Add `-s` (or `--signoff`) to your commits:

```bash
git commit -s -m "feat(provider): add streaming for Anthropic"
```

Git will append a `Signed-off-by: Your Name <you@example.com>` trailer from your configured `user.name` and `user.email`. The email **must match an email verified on your GitHub account**.

Verify your identity:

```bash
git config --global user.name "Your Real Name"
git config --global user.email "verified-email@example.com"
```

### Automatic sign-off (recommended)

Set up a global git hook so every commit is signed off automatically — no more "oops, forgot `-s`".

Create `~/.config/git/template/hooks/prepare-commit-msg`:

```bash
#!/bin/bash
NAME=$(git config user.name)
EMAIL=$(git config user.email)
if [ -z "$NAME" ] || [ -z "$EMAIL" ]; then
    exit 1
fi
git interpret-trailers --if-exists doNothing \
    --trailer "Signed-off-by: $NAME <$EMAIL>" \
    --in-place "$1"
```

Activate it:

```bash
chmod +x ~/.config/git/template/hooks/prepare-commit-msg
git config --global init.templateDir ~/.config/git/template
# For repos already cloned:
git config core.hooksPath ~/.config/git/template/hooks
```

### Fixing missing sign-offs

Last commit:

```bash
git commit --amend --signoff
git push --force-with-lease
```

A whole branch:

```bash
git rebase --signoff main
git push --force-with-lease
```

### Why DCO and not CLA?

The DCO certifies *origin* without transferring *copyright*. It's the lightest legally clean option, used by the Linux Kernel, Docker, GitLab and most major OSS projects. If galdor ever needs a CLA (e.g. for dual licensing), migration is straightforward via [CLA Assistant](https://cla-assistant.io).

## Reporting security issues

Do **not** open public issues for security vulnerabilities. Email the maintainers privately (contact will be added to `SECURITY.md` once published). Expect an acknowledgement within 72 hours.
