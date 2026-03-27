# SECURITY

## Overview

Super-Cache is designed for trusted-network deployments. Peer-to-peer links are authenticated with HMAC. Client traffic can require AUTH. TLS support exists in configuration but requires explicit certificate/key deployment choices.

## Threat Model

### Protected in v1.0

- Unauthorized peer join attempts without shared secret.
- Unauthorized client command execution when `auth_password` is configured.
- Basic management endpoint misuse when secret/socket permissions are configured correctly.

### Not fully protected in v1.0 defaults

- Traffic confidentiality without TLS or network encapsulation.
- Fine-grained command authorization (no ACL model).
- Brute-force mitigation (rate limiting is external responsibility).

## Peer Authentication

Peer auth uses challenge-based HMAC-SHA256 verification:

1. Peer handshake starts with HELLO/HELLO_ACK.
2. Acceptor issues random challenge nonce.
3. Connector returns HMAC digest computed with `shared_secret`.
4. Acceptor verifies digest and sends ACK.

Security properties:

- Secret is never transmitted directly.
- Captured traffic does not reveal secret material.
- Random challenge limits replay of static packets.

Validation requirements:

- `shared_secret` must be at least 32 characters.
- Mismatched/invalid proof closes auth flow.
- Failures should not leak internals.

## Client Authentication

When `auth_password` is set:

- Unauthenticated clients are restricted.
- AUTH is required before normal command set.
- Failure returns `WRONGPASS`.
- Session remains unauthenticated until successful AUTH.

Operational guidance:

- Use random high-entropy passwords (32+ chars).
- Restrict client network exposure via firewall.
- Consider external rate limiting/load balancer protections.

## Secret Management

Recommendations:

- Keep `shared_secret` and `auth_password` out of source control.
- Load through secure config management.
- Restrict read permissions on config files.
- Rotate secrets with controlled maintenance plan.

Validation task used during audit:

```bash
rg "SharedSecret|AuthPassword|shared_secret|auth_password" /opt/supercache/internal
```

Goal: ensure no sensitive value is logged directly.

## TLS and Encryption

- TLS-related config fields exist for client and peer listeners.
- If TLS is not enabled, use secure private networks (VPN, WireGuard, overlay).
- For internet-facing client access, terminate TLS at trusted proxy if direct TLS is not configured.

## Network Segmentation

Recommended boundaries:

- Peer port (`7379` default): allow only cluster nodes.
- Client port (`6379` default): allow application subnets only.
- Mgmt socket: filesystem ACL only; do not expose broadly.
- Mgmt TCP (if enabled): bind loopback, tunnel when remote admin is needed.

Example iptables sketch:

```bash
iptables -A INPUT -p tcp --dport 7379 -s 10.10.0.0/24 -j ACCEPT
iptables -A INPUT -p tcp --dport 7379 -j DROP
iptables -A INPUT -p tcp --dport 6379 -s 10.20.0.0/24 -j ACCEPT
iptables -A INPUT -p tcp --dport 6379 -j DROP
```

## Logging and Auditability

- Use structured logging for ingestion (`json`/`logfmt`).
- Keep warning/error logs centralized.
- Watch for auth failures, replication drops, and bootstrap abort events.

Suggested alerts:

- repeated peer auth failures
- repeated client AUTH failures
- sustained replication drop warnings
- frequent bootstrap retries

## Incident Response Security Checklist

1. Verify current cluster member list.
2. Check for unknown peer addresses.
3. Rotate secrets if compromise is suspected.
4. Rebuild trust boundary (firewalls, credentials, certs).
5. Confirm nodes rejoin with expected identities.

## Vulnerability Reporting

To report a security vulnerability in Super-Cache, contact Faisal Hanif directly by email at imfanee@gmail.com with the subject line: Super-Cache Security Vulnerability Report.
Please include in your report: a description of the vulnerability, the affected version or commit, steps to reproduce, and your assessment of the potential impact.
Do not disclose security vulnerabilities publicly before the author has had a reasonable opportunity to assess and address them. A reasonable disclosure window is 90 days from the date of your initial report.
Security reports are handled by Faisal Hanif personally. There is no bug bounty programme at this time. If the vulnerability is confirmed and resolved, you will be credited in the release notes unless you prefer to remain anonymous.
By reporting a vulnerability you agree that your report is submitted under the terms of responsible disclosure and that you will not exploit the vulnerability or disclose it to third parties during the disclosure window.


## Licensing Notice

Super-Cache is free to use in unmodified form for any purpose. This document is provided under the Super-Cache Software Licence. Contact: Faisal Hanif | imfanee@gmail.com.
