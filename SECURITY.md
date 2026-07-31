# Security Policy

## Experimental status

lamigrate is experimental software that does not yet claim production-safety
guarantees. Use at your own risk until a release is independently certified.

## Reporting vulnerabilities

If you discover a security vulnerability, please report it responsibly:

1. **Do not** open a public GitHub Issue for security vulnerabilities.
2. Email the maintainers or use GitHub's private vulnerability reporting.
3. Include:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact
   - Suggested fix (if any)

We will acknowledge receipt within 72 hours and provide a timeline for a fix.

## Scope

Security-relevant areas include:
- SQL injection through migration files (migration files are trusted source code)
- Advisory lock bypass or deadlock
- Metadata table corruption
- Credential leakage in logs, errors, or output
- Race conditions in concurrent migration execution

## Out of scope

- SQL injection through migration files is explicitly out of scope — anyone who
  can modify migration files has full database access by design.
- Denial-of-service through excessively large migration files (mitigated by
  file-size limits, not a security boundary).
