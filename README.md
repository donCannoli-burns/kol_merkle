# kol_merkle

Early **Go + SQLite** prototype of a provenance-preserving, content-addressed MediaWiki XML compiler. The repository is intentionally narrower than the 155-node Context Tree Studio planning scaffold. **Do not treat planned roles, encryption, browser discovery, external sync, or agent endpoints as implemented.**

## Intended workflow

Requires Go 1.23+ and dependency download for the pure-Go SQLite driver `modernc.org/sqlite`.

```sh
go mod download
go test ./...
go run ./cmd/kol-merkle compile -xml testdata/mini-wiki.xml -origin fixture://kol -db example.sqlite
go run ./cmd/kol-merkle verify -db example.sqlite
go run ./cmd/kol-merkle search -db example.sqlite -q undead
```

The CLI never fetches remote pages. Input is local MediaWiki XML supplied explicitly by the operator. Original wikitext is stored unchanged as SHA-256 addressed blobs; all revisions are retained. Current snapshots select the newest timestamp per page within the supplied export. Snapshot roots cover **only pages in that export**, not the full upstream wiki. Roots use a versioned binary Merkle construction over sorted leaf records.

### Current limits

- The importer retains wiki revision text; template expansion, HTML rendering, backlinks, categories, Cargo entity IDs, media files, historical queries, permissions, and agent APIs are **not built**.
- The XML file size limit defaults to 2 GiB; revision text is limited to 16 MiB after decoding. This is an experimental importer, not yet hardened against all hostile XML or filesystem failures.
- Search indexes raw revision text through SQLite FTS5. Historical revisions are searchable, not only the latest.
- The `verify` command recomputes hashes and snapshot roots. It is not a signed attestation and cannot establish upstream authenticity on its own.
- Repository code is not a distribution of wiki content. Confirm source licensing, access rules, and consent before importing or redistributing third-party wiki archives.

## Planning and provenance

This bootstrap is based on the user's Context Tree Studio `kol_merkle_V0.0.0.0-1_full_project.zip` (virtual scaffold, revision 15). Upstream architecture references: `gausie/kol-wiki-mirror`, `gausie/kol-entity-ids`, `gausie/kol-wiki-proxy`. These are design references, not vendored source.

See `docs/ARCHITECTURE.md` and `docs/ROADMAP.md` for implemented boundaries and future phases.

See `docs/BUILD_LEDGER.md` for the precise verification limits of this initial candidate.
