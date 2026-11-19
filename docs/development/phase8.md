# Phase 8 — signed artifact staging

This phase adds real HTTPS download and byte verification, **not software installation**.
The subsequent [Phase 9A](phase9.md) supplies an explicit Windows test executor;
Phase 8's staging command itself never executes the MSI.
The Agent service and central dashboard still accept only their existing simulations.
See the [remaining phases](roadmap.md) for MSI integration and Windows acceptance.

## Authorization

`contracts.ArtifactAuthorization` is a versioned envelope binding an enrolled device ID,
job ID, issued-at/expiry, version, package type, URL, SHA-256, exact size and key ID.
The signature is Ed25519 over the Go canonical JSON struct with the signature cleared
and domain `kiosentra-artifact-authorization-v1`. Consumers should use the shared Go
signer, rather than independently serializing JSON. The validity window is at most 24
hours, checked before and after download. Only MSI metadata is admitted in this phase.

Trust is an explicitly provisioned key-ID/public-key map; unknown or removed keys fail
closed. Simulation key IDs and simulation signatures are rejected. Use a distinct release
key, held on an operator signing machine, never on an endpoint. The envelope authorizes
staging; Phase 9 must add execution admission, persistent replay protection and leases.
Staging the same authorization twice creates two distinct directories and never executes.

An unsigned input file has this shape (replace **every** example value):

```json
{
  "schemaVersion": 1,
  "deviceId": "<enrolled UUID>",
  "jobId": "<fresh UUID>",
  "issuedAt": "<current UTC RFC3339 timestamp>",
  "expiresAt": "<UTC RFC3339 timestamp within 24 hours>",
  "release": {
    "version": "1.0.0",
    "packageType": "msi",
    "url": "https://packages.example/kiosk-1.0.0.msi",
    "sha256": "<64 hex characters calculated from the actual MSI>",
    "signature": "",
    "keyId": "release-2026-1",
    "sizeBytes": 12345
  }
}
```

On the signing machine, use a private file containing a base64 Ed25519 64-byte private
key (seed plus public key). Unix permissions must be owner-only; on Windows provision
an operator-only ACL. This is **not** an Authenticode certificate and does not replace
the native package-signing acceptance required in Phase 10.

```sh
go run ./apps/agent/cmd/kiosentra-sign-artifact \
  --input=/absolute/unsigned.json \
  --private-key-file=/absolute/private-release.key \
  --output=/absolute/signed.json
```

Output creation is exclusive: existing files are never replaced. Private keys do not go
in command-line arguments or output. Provision the matching 32-byte public key separately.

## Download

Create an empty, owner-only staging root, or on Windows provision private NTFS ACLs for
the executing account. The root must already exist and be an absolute, non-symlink
directory. Keep its parent directories under trusted ownership too.

```sh
go run ./apps/agent/cmd/kiosentra-stage \
  --authorization=/absolute/signed.json \
  --device-id='<enrolled UUID>' \
  --key-id=release-2026-1 \
  --public-key='<base64 public key>' \
  --origins=https://packages.example \
  --directory=/absolute/private-staging
```

Success prints JSON with `path` and `installed: false`. The default maximum is 256 MiB
and five minutes; `--max-bytes` permits up to 2 GiB and `--timeout` up to 15 minutes.
Authorization expiry can shorten the timeout. Origins are exact HTTPS scheme/host/port
matches with no path or trailing slash; provision only trusted artifact hosts. No network
request occurs until signature, device, lifetime, origin and local size checks pass.

The downloader uses system TLS verification, no Agent bearer credential, no cookie jar,
no environment proxy and no redirects. It requires HTTP 200, unencoded bytes, matching
declared size and SHA-256, including chunked responses. It never extracts or executes.
Failed attempts remove their private staging directory. Successful output contains
`artifact.msi` and `authorization.json`; files are flushed before publishing the MSI name.

## Acceptance boundary

- Tests exercise real local TLS servers, bad signatures/binding, expiry, size/hash,
  redirects, TLS trust, timeout and failed-attempt cleanup.
- Host tests and Windows cross-build do not validate NTFS permissions or Windows runtime.
- A crash can leave an incomplete private directory. No later run resumes or trusts it;
  an operator may remove abandoned staging directories after confirming no run uses them.
- There is no aggregate disk quota, cleanup scheduler or download resumption yet.
- Before execution, Phase 9 must reverify bytes and inspect MSI product identity/version,
  record durable intent, handle uncertain installer results and demonstrate native recovery.
- This phase neither revokes previously issued signatures centrally nor supplies a
  production key-rotation authority. Remove a trusted key locally to stop future staging.
