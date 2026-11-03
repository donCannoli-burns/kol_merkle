# Verification contract — M0 verification repair / v2

The original M0 (`binary-merkle-sha256-v1`) root covered only one revision per page.
It did **not** bind historical revision existence, timestamps, or the source XML
export identity. `verify` deliberately rejects v1 snapshots rather than applying
v2 assurance language to an archive whose evidence cannot prove that claim.
Existing v1 data is not deleted. Reimport original exports to create v2 snapshots.

## v2 commitment

Each imported XML source creates a `snapshot-sha256-v2` root committing to:

1. The existing v1 binary Merkle root of canonically sorted *current page* leaves,
   preserving the leaf/node definitions and their original domain separation.
2. A streaming `history-sha256-v2` digest of **every revision present in that
   particular export**, sorted by numeric page ID and revision ID. Each record
   includes source origin, page ID, namespace, title, revision ID, captured
   timestamp and SHA-256 of its decoded wikitext. This history digest is **not**
   itself a Merkle tree.
3. SHA-256 of the **exact input XML export bytes** and its byte length.

The v2 root hashes these fields under a distinct versioned domain, with length
framing. Export order affects the source-byte digest, even when the wiki records
are logically equivalent. The page selection rule is newest timestamp, then
highest revision ID on a tie. No historical export can be inferred complete
relative to the upstream wiki from these records alone.

Stored wikitext SHA-256 addresses **XML-decoded UTF-8 text bytes**. XML entity
escapes and CDATA markup are not retained byte-for-byte as individual text
fields. The export's SHA-256 anchors the original XML representation separately.

## Verification scope

`verify` checks immutable object bytes, revision relationships, page identity,
FTS5 row-to-revision/body consistency including strict storage classes
(`page_key`, `title`, `body` must be SQLite TEXT; `source_revision_id` must
be SQLite INTEGER), FTS5 internal integrity, v2 historical
memberships, current-selection consistency, all roots and all retained revision
coverage. It does not silently repair an inconsistent database.

The optional `verify -root ROOT -xml EXPORT.xml` additionally recomputes the
original XML bytes and independently checks the recorded source hash and length.
Without the original export, the source digest is committed but cannot be
independently recovered from the flattened SQLite records.

This protects against accidental loss, inconsistent edits, and basic archive
alteration relative to its retained commitments. It is **not** authentication:
an adversary able to rewrite the complete database can recompute all unkeyed
hashes and fabricate a self-consistent replacement. Preserve roots outside the
DB, use externally trusted signatures when needed, and retain source exports.

## M0-R01 XML content admission boundary

M0 imports exactly one inline main-slot wikitext `<text>` element per revision. A
present and genuinely empty inline `<text/>` is distinct from absent content.
Missing text, `deleted` or `location` markers, empty external-ID stubs,
empty text with a positive declared byte size, additional `<content>` entries,
wrapped `<slots>`, duplicate text elements, nested XML elements in text, and
explicit non-wikitext content model/format are rejected before the import
transaction can commit. Exports that require those features must be handled
by a future versioned representation; M0 does not fill them with empty text.
The scope is an intentional fail-closed subset of MediaWiki XML, not full export
coverage or a new security guarantee for untrusted XML.

## Parser resource scope

**M0 ingestion policy: only locally supplied, trusted XML exports are supported.**
Do not process attacker-controlled or unknown-provenance XML as if its decoded
memory use were bounded. The importer is not an XML sandbox. The local-only
entrypoint is an acquisition boundary, not a security boundary: a downloaded
file may still be hostile.

The parser validates individual revision sizes, total page text and revision
count during incremental page assembly; an additional raw XML-run cap prevents
one continuous text run from growing without limit in the XML decoder.
`encoding/xml` may still allocate large tokens for deliberately fragmented or
CDATA-rich hostile input. The caller must supply a bounded, trusted source and
appropriate process memory limits. M0 is not certified as an adversarial XML
sandbox. All imports remain offline and rollback on failure.

## Acceptance tests

Integration tests on disposable databases must fail verification after deleting
historical revisions, changing revision timestamps, inserting forged FTS text,
duplicating FTS rows, changing current/history snapshot entries, or modifying
source byte-length metadata. Reimporting a known revision with the same decoded
text but different timestamp/title must abort and rollback. V1 snapshots must
fail closed. The Go/SQLite test suite must pass in the pinned CI environment.

## Independent re-audit follow-up

A textual FTS5 revision ID such as `101.0` can compare equal to integer `101`
under SQLite numeric affinity, while `database/sql` rejects scanning it into
Go `int64`. M0 now checks the SQLite storage class of each matched FTS field
before declaring verification success. Disposable-database regressions include
`101.0` (TEXT), `101` (TEXT), `101.0` (REAL), and BLOB body storage, in addition
to the original eight integrity mutations.

A fixed XML input byte limit, per-page limits, and uninterrupted raw-run caps
do not impose a strict heap limit. Fragmented CDATA can still trigger large
`encoding/xml` allocations before per-revision checks run, and the compiler's
page map grows with imported page count. A dedicated hostile-XML sandbox and
strict memory/page budgets are deferred, **not** represented as completed.
