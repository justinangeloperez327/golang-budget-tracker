# Agent instructions

- Implement only the requested implementation group. Respect the locked Go + templ + HTMX architecture.
- All money must be represented and calculated exactly; do not use floating point for stored currency amounts.
- Preserve per-user workspace authorization and prevent cross-account record access.
- Prefer simple, explicit Go and SQL over premature interfaces or microservices.
- Test each change and document limitations. Do not misrepresent unrun CI as passing.
- Use Node 24 for browser/asset tooling. Select GitHub Actions native Node 24 action versions.
- Never configure automatic production deployment or publish/release packages.
- The user prefers direct commits to main without pull requests when explicitly authorized.
