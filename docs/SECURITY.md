# Security Notes

## Trust Model

`caw` release artifacts should be trusted in this order:

1. versioned Git tag
2. GitHub release asset URL under `derekurban/codex-auth-wrapper`
3. `checksums.txt`
4. optional signature and certificate assets

Package-manager channels should point back to the same canonical GitHub release binaries.

## Verify Checksums

Windows PowerShell:

```powershell
Invoke-WebRequest https://github.com/derekurban/codex-auth-wrapper/releases/download/vX.Y.Z/checksums.txt -OutFile checksums.txt
Invoke-WebRequest https://github.com/derekurban/codex-auth-wrapper/releases/download/vX.Y.Z/caw_vX.Y.Z_windows_amd64.zip -OutFile caw.zip
$expected = (Get-Content checksums.txt | Where-Object { $_ -match "caw_vX.Y.Z_windows_amd64.zip" }).Split()[0]
$actual = (Get-FileHash -Algorithm SHA256 caw.zip).Hash.ToLowerInvariant()
if ($expected.ToLowerInvariant() -ne $actual) { throw "Checksum mismatch" }
```

## Signature Verification

If the repo uses cosign keyless signing, keep the generated `checksums.txt.sig` and `checksums.txt.pem` assets beside the release. Verification can then be performed with a command like:

```bash
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  checksums.txt
```

## Operational Guardrails

- publish only Windows artifacts until the broker/session runtime is portable beyond Windows
- prefer GitHub OIDC keyless signing over long-lived signing keys
- scope publication tokens to a single downstream repo or registry when possible
- pin GitHub Actions by SHA before promoting the workflow to production
- publish a new tag for real fixes instead of mutating existing binaries
