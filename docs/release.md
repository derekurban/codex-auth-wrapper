# Release Runbook

This runbook assumes the canonical release source is GitHub Releases backed by GoReleaser. Selected channels: `github, go-install, powershell-installer`.

## Release Checklist

| Owner | Command or action | Expected result |
| --- | --- | --- |
| Release operator | `git tag vX.Y.Z && git push origin vX.Y.Z` | Release workflow starts |
| CI | `go test ./...` and `go vet ./...` | Release is gated by tests and vet |
| CI | `goreleaser release --clean` | Archives and `checksums.txt` are published |
| CI | `cosign sign-blob dist/checksums.txt` | Signature and certificate assets are uploaded |
| Release operator | Review release notes and install commands | GitHub Release, `go install`, and the PowerShell installer all point at the same version |

## Required Secrets

| Secret | Required when | Least privilege | Why |
| --- | --- | --- | --- |
| `GITHUB_TOKEN` | Always | Same-repo release permissions only | Upload release assets and notes |

GitHub OIDC keyless signing is the default recommendation. If the repo cannot use OIDC, replace that path with `COSIGN_PRIVATE_KEY` and `COSIGN_PASSWORD`.

## Downstream Channel Notes

- Current published install surfaces:
  - GitHub Release assets
  - `go install github.com/derekurban/codex-auth-wrapper/cmd/caw@<tag>`
  - `scripts/install.ps1`

## Rollback

1. Disable or pause downstream publication if a broken tag has already started propagating.
2. Delete or mark the GitHub release as superseded only if the broken artifacts must not be consumed.
3. Publish a fresh tag instead of mutating binaries for the same version.
4. Update package-manager manifests to the new tag only after the replacement release is verified.

## Troubleshooting

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| Checksum mismatch | Wrong asset URL or replaced artifact | Regenerate the release under a new tag and update channel metadata |
| Missing architecture | Build matrix or archive template mismatch | Check `.goreleaser.yaml` targets and release asset names |
| Installer downloaded the wrong asset | Artifact naming drifted from the release contract | Re-check `.goreleaser.yaml` and `scripts/install.ps1` together |
| `--version` output is wrong | Linker flags or version variables differ from the binary | Update the Go version injection path in `.goreleaser.yaml` |
