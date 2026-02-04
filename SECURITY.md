# Security Policy

## Supported Versions

| Version | Supported          |
|---------|--------------------|
| latest  | :white_check_mark: |
| < latest | :x:               |

Only the latest released version receives security fixes. Users are encouraged to upgrade promptly.

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues, discussions, or pull requests.**

Instead, use **GitHub's private vulnerability reporting** to report security issues:

1. Go to the [Security Advisories](https://github.com/szibis/metrics-governor/security/advisories) page.
2. Click **"Report a vulnerability"**.
3. Fill in the details and submit.

This ensures the report stays private until a fix is available.

### What to Include

- Description of the vulnerability and its potential impact.
- Steps to reproduce or a proof of concept.
- Affected version(s).
- Any suggested fix, if you have one.

### What to Expect

- **Acknowledgement** within 3 business days.
- A plan for a fix or a request for more information within 7 business days.
- A coordinated disclosure timeline agreed upon with the reporter before any public announcement.
- Credit in the release notes (unless you prefer to remain anonymous).

## Security Measures

This project uses automated security tooling in CI:

- **govulncheck** — Go dependency vulnerability scanning (call-graph aware)
- **Trivy** — filesystem and container image CVE scanning
- **gitleaks** — secret detection in git history
- **checkov** — infrastructure-as-code misconfiguration scanning
- **CodeQL** — static analysis for code-level vulnerabilities
- **Dependabot** — automated dependency update PRs
- **Secret scanning with push protection** — blocks commits containing secrets

## Scope

The following are in scope for security reports:

- The `metrics-governor` binary and its dependencies.
- The Helm chart and its default configuration.
- The Docker image and its base layers.
- CI/CD workflow configurations that could lead to supply chain issues.

The following are **out of scope**:

- Vulnerabilities in upstream dependencies that are already publicly disclosed (please open a regular issue instead).
- Denial of service through expected high-volume metric ingestion (this is a performance concern, not a security vulnerability).
