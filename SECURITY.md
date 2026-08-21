# Security Policy

## Supported versions

EasyDeploy is pre-1.0 software under active development. Security fixes are
applied to the latest release and the `main` branch. Please make sure you are on
the most recent version before reporting an issue.

| Version | Supported |
| ------- | --------- |
| latest `0.x` | ✅ |
| older `0.x`  | ❌ |

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
discussions, or pull requests.**

Instead, report them privately through GitHub's
[**Report a vulnerability**](https://github.com/davanntet-os/EasyDeploy/security/advisories/new)
form (Security → Advisories → Report a vulnerability). This keeps the details
private until a fix is available.

Please include, as much as you can:

- A description of the vulnerability and its impact.
- Steps to reproduce, or a proof-of-concept.
- Affected version(s) / commit, and your environment.
- Any suggested remediation.

We will acknowledge your report, keep you updated on the fix, and credit you in
the release notes unless you prefer to remain anonymous.

## Scope & hardening notes

EasyDeploy manages Docker daemons and can expose containers publicly, so a
misconfiguration can be sensitive. When self-hosting:

- Always set a strong `EASYDEPLOY_ADMIN_PASSWORD` and a unique
  `EASYDEPLOY_SECRET_KEY` (the server refuses to start without them). The secret
  key encrypts registry and remote-host credentials at rest — rotating it makes
  existing ciphertext unreadable.
- Prefer **SSH** for remote environments (no open Docker port). For TCP, use
  **mutual TLS with a CA**; never expose an unauthenticated `:2375` daemon to an
  untrusted network.
- Restrict who can reach the API and the xDS control plane (`:18000`).

Reports about the security of the underlying Docker daemon, Envoy, or Postgres
should go to those projects; we're happy to help with anything specific to how
EasyDeploy configures them.
