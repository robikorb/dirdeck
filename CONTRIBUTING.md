# Contributing

## Before opening a change

- Do not include credentials, host paths, databases, mounted files, or private
  screenshots.
- Keep filesystem tests on `t.TempDir()` or the tracked disposable fixtures.
- Preserve the volume-ID plus relative-path boundary.
- Enforce permissions in the backend, not only by disabling UI controls.
- Add tests for every mutating path and failure state.
- Update `CHANGELOG.md` and the relevant document under `docs/`.

## Development checks

```bash
cd frontend
npm ci
npm run lint
npm run build

cd ../backend
go test ./...
go vet ./...

cd ..
docker compose config
docker compose build
```

## Pull requests

Explain the user-visible behavior, safety implications, migrations, tests, and
documentation changes. For file operations, describe behavior on read-only
volumes, symlinks, conflicts, cancellation, restart, and partial failure.

Do not make a breaking database or configuration change without a migration
and upgrade/rollback notes.
