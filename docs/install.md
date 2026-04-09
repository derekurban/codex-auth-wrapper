# Install caw

`caw` is currently released as a Windows-first CLI.

## Channels

### Go

```powershell
go install github.com/derekurban/codex-auth-wrapper/cmd/caw@latest
```

### Windows PowerShell installer

```powershell
iwr https://raw.githubusercontent.com/derekurban/codex-auth-wrapper/main/scripts/install.ps1 -useb | iex
```

The installer:
- downloads and verifies the tagged release asset
- installs `caw.exe`
- writes a `caw.cmd` shim beside it
- adds the install directory to your user `PATH`
- updates the current session `PATH` so `caw` works immediately

## Verify

After installation, run:

```powershell
caw.exe --version
```

## Uninstall

### Go

Remove the installed binary from your Go bin directory:

```bash
rm -f "$(go env GOPATH)/bin/caw"
```

### Windows PowerShell installer

Remove the installed binary from the directory you passed as `InstallDir`. The default location is:

```powershell
Remove-Item "$HOME\\bin\\caw.exe"
```

## Troubleshooting

| OS or symptom | Likely cause | Fix |
| --- | --- | --- |
| Windows installer cannot find the asset | Release archive name changed | Confirm `.goreleaser.yaml` naming matches the install script |
| `go install` fetched the wrong entrypoint | Main package path differs from the docs | Update the documented `go install` target |
| PATH does not include the install directory | User bin dir is not exported | Add the install location to PATH and retry |
