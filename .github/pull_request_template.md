## What and why

<!-- What changes, and what problem it solves. The "why" is the part
     that ages well — the diff already shows the "what". -->

## Checklist

- [ ] `go test ./...` passes
- [ ] `gofmt` clean
- [ ] Playwright e2e passes, or the change can't affect it
- [ ] New behaviour is covered by a test
- [ ] No real log data in fixtures, tests, or this description
- [ ] Version bumped in `build.sh`, `frontend/package.json`, and
      `main.go` (both sites), if user-visible
- [ ] `CHANGELOG.md` updated under Unreleased, if user-visible

### For a new ingest format

- [ ] REQ-DT row added to `REQUIREMENTS.md`
- [ ] Fixture added (synthetic — see CONTRIBUTING.md)
- [ ] Routing asserted in `internal/ingest/fixtures_test.go`
- [ ] E2e case in `frontend/e2e/formats.spec.js`

### For a new dependency

- [ ] License is permissive (MIT/BSD/Apache-2.0/ISC/MPL-2.0) — **not**
      GPL or AGPL
- [ ] Added to `THIRD-PARTY-NOTICES.md`
