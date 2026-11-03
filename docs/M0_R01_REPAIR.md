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
- Reject explicit content models/formats outside inline wikitext, duplicated
  revision metadata, unknown revision children and contradicting text byte sizes.
- Compare declared `bytes` to XML-decoded UTF-8 byte length, including
  escaped entities and multibyte Unicode.
- Keep root validation as a separately recorded scope decision (R01-A04).
- Keep v2 Merkle algorithms, history semantics, SQLite schema, and FTS unchanged.
- Exercise decoder admission matrix and transactional rollback after a valid page.

## Required acceptance

1. Stdlib XML parser regressions pass.
2. Real-driver full suite and `go vet ./...` pass on exact PR head.
3. CLI compile/verify/search smoke test with known fixture plus negative input,
   or documented reason why that evidence is deferred.
4. Independent review of the admission boundary and unchanged hash contracts.
5. Owner release decision separate from technical acceptance.

## Independent audit follow-up

A subsequent exact-head source re-audit at
`985be9043b41b7362393ddacad673361dd76bcc3` found R01-R02-01:
`encoding/xml` permits duplicate attribute names in a token, allowing
the last `bytes` value to conceal an earlier contradictory declaration.
The follow-up checks repeated expanded `xml.Name` identities before decoding
any text attribute values. Regression cases cover duplicate `bytes`, `id`,
`sha1` and `xml:space` attributes and transactional rollback of an
ambiguous byte declaration. These fixtures are intentionally malformed XML;
the objective is to reject, not reinterpret, ambiguous input.
Independent re-audit of the new head remains required.

The independent report at head `e26687bc315c9cf80cb5f004d7aacebdb56ca34e`
reported `GO_HARDENING_REQUIRED` for R01-A01 (duplicate metadata),
R01-A02 (declared bytes), and R01-A03 (unknown revision child). The scoped
follow-up adds explicit checks and regression cases, including integration
rollback tests. R01-A04 (root validation) is deferred as a separate policy
question; M0 does not claim a complete XML schema validator.

This branch is a **candidate**, not release authorization. The existing
trusted-input-only rule and known hostile-XML heap limitations remain.
