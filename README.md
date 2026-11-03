# kol_merkle

Early **Go + SQLite** prototype of a provenance-preserving, content-addressed MediaWiki XML compiler. The repository is intentionally narrower than the 155-node Context Tree Studio planning scaffold. **Do not treat planned roles, encryption, browser discovery, external sync, or agent endpoints as implemented.**

## Intended workflow

Requires Go 1.23+ and dependency download for the pure-Go SQLite driver `modernc.org/sqlite`.

```sh
go mod download
go test ./...
go run ./cmd/kol-merkle compile -xml testdata/mini-wiki.xml -origin fixture://kol -db example.sqlite
go run ./cmd/kol-merkle verify -db example.sqlite
go run ./cmd/kol-merkle verify -db example.sqlite -root SNAPSHOT_ROOT -xml testdata/mini-wiki.xml
go run ./cmd/kol-merkle search -db example.sqlite -q undead
```

The CLI never fetches remote pages. Input is local MediaWiki XML supplied explicitly by the operator. XML-decoded UTF-8 wikitext is stored as SHA-256 addressed blobs; all revisions are retained. The SHA-256 of the byte-exact original XML export is separately bound to each v2 snapshot. Current snapshots select the newest timestamp per page within the supplied export. Snapshot roots cover **only pages in that export**, not the full upstream wiki. V2 snapshots bind the v1 current-page binary Merkle root, complete imported revision history and exact source-export digest under a new domain-separated commitment.

### Current limits

- The importer retains **inline main-slot wikitext only**. Missing, deleted, externally located or stub revision content, duplicated text elements, non-wikitext models/formats, and additional/wrapped content slots are rejected rather than silently hashed as empty text. A genuine inline empty `<text/>` is allowed. This is a deliberately narrow M0 input contract, not complete MediaWiki XML support. Template expansion, HTML rendering, backlinks, categories, Cargo entity IDs, media files, historical queries, permissions, and agent APIs are **not built**.
- **M0 accepts trusted, locally supplied XML exports only.** Downloaded or unknown-provenance exports are not automatically trustworthy. The XML file size limit defaults to 2 GiB; individual revision text is limited to 16 MiB, cumulative text per page to 128 MiB, and long raw XML spans to 32 MiB. Fragmented CDATA can bypass the raw-run limit, and the per-page map grows with page count; these limits **do not** provide a strict memory guarantee. Do not process hostile XML without separate process isolation and resource budgets.
- Search indexes raw revision text through SQLite FTS5. Historical revisions are searchable, not only the latest.
- The `verify` command checks blob hashes, all revisions, FTS5 records/index integrity including storage types required by Go search, historical membership, and v2 snapshot roots. V1 snapshots intentionally fail closed. Optional `-root` and `-xml` independently match a source export. It is not a signed attestation and cannot establish upstream authenticity on its own.
- Repository code is not a distribution of wiki content. Confirm source licensing, access rules, and consent before importing or redistributing third-party wiki archives.

## Planning and provenance

This bootstrap is based on the user's Context Tree Studio `kol_merkle_V0.0.0.0-1_full_project.zip` (virtual scaffold, revision 15). Upstream architecture references: `gausie/kol-wiki-mirror`, `gausie/kol-entity-ids`, `gausie/kol-wiki-proxy`. These are design references, not vendored source.

See `docs/ARCHITECTURE.md` and `docs/ROADMAP.md` for implemented boundaries and future phases.

See `docs/VERIFICATION_CONTRACT.md` for the v2 hash, history, source-byte, and remaining threat limits. See `docs/BUILD_LEDGER.md` for the original M0 audit baseline.
