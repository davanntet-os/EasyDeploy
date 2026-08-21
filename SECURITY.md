# Security Policy

## Supported Versions

This project is under active development. Security fixes are applied to the
latest version on the default branch. Please make sure you are running the most
recent release before reporting an issue.

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |
| older   | :x:                |

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, report them privately using one of the following:

- Use GitHub's [private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
  ("Report a vulnerability" under the repository's **Security** tab), or
- Email the maintainers with details.

Please include as much of the following as you can:

- A description of the vulnerability and its impact
- Steps to reproduce (proof-of-concept, affected endpoint or file)
- The version / commit you tested against
- Any suggested remediation

You can expect an initial response within **5 business days**. We will keep you
informed of progress toward a fix and may ask for additional information. Once
resolved, we are happy to credit you in the release notes unless you prefer to
remain anonymous.

## Scope and Hardening Notes

This application is designed for **local or trusted-network use** and ships with
some deliberate limitations. The following are known and documented, not
vulnerabilities in themselves:

- **No authentication.** Anyone who can reach the server can browse, upload,
  and delete files within the configured root. Add authentication and run
  behind HTTPS before exposing it to untrusted networks.
- **Binds to `0.0.0.0` in the container**, making it reachable from the
  network. Restrict this with firewall rules or a reverse proxy as needed.

Genuine security concerns we **do** want reported include, for example:

- Path-traversal or any way to read/write/delete files **outside** the
  configured root directory (`FILEBROWSE_ROOT`)
- Remote code execution or injection
- Denial of service reachable within intended (trusted) use

Thank you for helping keep this project and its users safe.
