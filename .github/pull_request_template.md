## Summary

Describe the user-visible change.

## Safety

- [ ] Backend permissions are enforced, not only hidden in the UI.
- [ ] Filesystem tests use disposable temporary roots.
- [ ] Read-only, traversal, symlink, conflict, cancellation, and partial-failure
      behavior were considered where relevant.
- [ ] No secrets, private paths, databases, or mounted user files are included.

## Verification

- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `npm run lint`
- [ ] `npm run build`
- [ ] `docker compose config`
- [ ] Documentation and `CHANGELOG.md` updated
