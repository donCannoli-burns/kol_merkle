# M0-R01 — XML content admission candidate

Source base: `1143f43e19bf3b8757b3c4db3688d8e5068b54ea` (merged M0 v2.1 `main`).

## Problem

The prior `Revision.Text string` decoding could not distinguish an empty
`<text/>` from absent, redacted, externally located or unrepresented revision
content. Hashing those as known-empty wikitext would preserve a false fact
even when the v2 Merkle verification checks passed.

## Bounded remedy

- Fail closed on unavailable main text and unsupported multi-slot/wrapped content.
- Allow genuine inline empty text, older inline-only exports, XML-escaped text,
  and CDATA inline wikitext.
- Reject explicit content models/formats outside inline wikitext.
- Keep v2 Merkle algorithms, history semantics, SQLite schema, and FTS unchanged.
- Exercise decoder admission matrix and transactional rollback after a valid page.

## Required acceptance

1. Stdlib XML parser regressions pass.
2. Real-driver full suite and `go vet ./...` pass on exact PR head.
3. CLI compile/verify/search smoke test with known fixture plus negative input,
   or documented reason why that evidence is deferred.
4. Independent review of the admission boundary and unchanged hash contracts.
5. Owner release decision separate from technical acceptance.

This branch is a **candidate**, not release authorization. The existing
trusted-input-only rule and known hostile-XML heap limitations remain.
