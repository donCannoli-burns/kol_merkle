# Build ledger — M0 candidate

Planning input: `kol_merkle_V0.0.0.0-1_full_project.zip`

- Input ZIP SHA-256: `f4ad5d6e8b33d63aa6555bfde20c7b609eb3d0f75e0f8458cd893918dcc96594`
- Planning project ID: `e3c1d7fd-3e5c-4149-8a49-ec202f667249`
- Planning revision: `15`; virtual nodes: `155`
- Indexed ZIP payload integrity: `12/12 PASS`; ZIP CRC integrity: `PASS`
- Go standard-library unit tests: `internal/wiki` and `internal/merkle` PASS
- SQLite DDL validated with Python sqlite3; FTS5 snippet query passed
- Full Go integration tests + `go vet` in GitHub Actions run `41582984843`: PASS
- PR-associated GitHub Actions run `41582990090`: PASS
- Offline local full-test limitation: runtime DNS cannot resolve `proxy.golang.org`; independent runner verified Go tests
- Release disposition: **M0_CANDIDATE__CI_AND_INDEPENDENT_REVIEW_REQUIRED**

This is a source-code bootstrap candidate, not a verified production release, a complete mirror, or a full implementation of Context Tree Studio's planning scaffold.
