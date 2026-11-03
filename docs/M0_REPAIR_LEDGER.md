# M0 repair ledger — verification-contract v2 candidate

Parent audited commit: `b70da8ae5294565617cee40fc18344a7a934d054`.
Independent report: `GO_HARDENING_REQUIRED`, findings M0-01 through M0-06.

## Changes mapped to findings

- M0-01: Snapshot captures the membership and metadata of **all imported revisions**;
  verification compares retained revision records and recalculates the history digest.
- M0-02: Verification compares FTS5 rows with immutable content objects and revisions,
  detects missing/extra/duplicate entries and executes FTS5's internal integrity check.
- M0-03: `snapshot-sha256-v2` roots commit historical metadata, export-byte SHA-256,
  source length and the existing current-page Merkle root. The original XML export
  can optionally be supplied for independent source-hash comparison.
- M0-04: Reimports with the same page/revision IDs but altered titles or timestamps
  fail and roll back even when decoded wikitext SHA-256 matches.
- M0-05: XML parser assembles revisions incrementally and checks per-revision,
  per-page and raw-run limits during decoding. This limits common amplification
  but is not yet a complete solution for adversarial fragmented or CDATA-heavy XML.
- M0-06: Wikitext object identity is explicitly defined as XML-decoded UTF-8;
  the exact raw XML bytes are committed separately. Regression tests cover
  entity escaping and CDATA representation.

## Test status

- `go test ./internal/wiki ./internal/merkle`: passed locally.
- All packages compile and `go vet ./...` passes with a **temporary stub** used
  only to stand in for the unavailable modernc.org/sqlite module during local
  static checking. The stub was not included in GitHub source.
- Full SQLite-backed `go test ./...`: **NOT run locally** due to DNS restrictions;
  GitHub Actions with the pinned real driver is required before promotion.
- SQL schema and critical FTS5 queries: independently exercised with Python
  `sqlite3`; these are not equivalent to executing the Go integration tests.

## Residual trust limits

`verify` checks a local database against its own retained commitments. It is not
an authenticity signature. A party who replaces the entire database and all
stored root records can produce a different internally consistent database.
Keep a trusted external snapshot root or signed manifest and the original XML
export where stronger provenance is required.

V1 snapshot records are preserved but intentionally fail closed under the new
`verify` contract. An original export must be reimported to establish v2
commitments. M0 retains a local-only import surface; online discovery, agents,
admin vault, and user authorization remain unimplemented.

Disposition before full CI and follow-up audit: `M0_V2_REPAIR_CANDIDATE_UNVERIFIED`.
