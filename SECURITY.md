# Security Policy

## Reporting a vulnerability

Please do **not** open a public issue for security vulnerabilities.

Report privately via **GitHub Security Advisories**: go to the repository's
**Security** tab → **Report a vulnerability**. You'll get an acknowledgement
within 72 hours.

If you can, include: the affected package/module, a minimal reproduction,
and the impact as you understand it. `galdor doctor` output and the galdor
version help too.

## Supported versions

The latest minor release line receives security fixes. Pre-1.0 tags are not
retro-patched — upgrade to the newest release.

## Scope notes

- The dashboard (`galdor ui`) binds to loopback by default and has no
  authentication; exposing it beyond localhost is an explicit operator
  decision (see `docs/ops.md`).
- `file_read` / `http_get` builtins are sandbox-gated by design (BaseDir
  confinement, host allowlists); a bypass of those guards is in scope and
  very much worth reporting.
- The project runs `gosec` and `govulncheck` in CI on every module; see
  `docs/security.md` for the standing self-assessment.
