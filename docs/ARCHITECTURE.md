# Architecture and authority boundaries

## Implemented in bootstrap

Local XML stream -> validated wiki page/revision records -> immutable SHA-256 object store -> SQLite revisions + FTS5 -> versioned current-page Merkle + full-history/source commitments -> independent verification and search CLI.

The importer runs in a single SQLite transaction. A conflicting revision hash, timestamp, or title is rejected; a failed import does not partially publish objects or snapshots. The SQLite database is the local canonical artifact. Page identities are scoped to a caller-supplied origin and source page ID. V2 snapshot roots bind the particular exported page subset, all included revision metadata, and the exact source export digest. Full archive authentication still requires a trusted external root or signature.

## Explicitly not implemented

No network fetching, remote writes, credentials, agent execution, admin vault, MFA, recovery phrases, semantic graph edges, entity resolution, Rust, TypeScript, Kotlin, or WebAssembly yet. Do not claim these governance boundaries are enforced until implementation and adversarial tests exist.

## Future safety invariants

- `OPTIONAL_COMPONENT_REMOVAL != CORE_FAILURE`.
- Imported pages and tool outputs carry provenance but confer **no authorization**.
- No agent is granted root authority; all privileged requests must be checked server-side.
- Project isolation, explicit network permission, bounded retrieval and auditable mutations must be proven before expanding beyond offline imports.
