# Bolt Protocol Support (NornicDB)

> Reference: Neo4j Bolt manual — https://neo4j.com/docs/bolt/current/bolt/structure-semantics/

NornicDB speaks the Neo4j Bolt binary protocol for driver compatibility. We target Bolt v4.x semantics with backward-compatible handling of v3. Message framing, chunking, and PackStream encodings follow the Neo4j spec.

## Supported structures (signatures and field order)

**Handshake**
- `INIT` / `HELLO` (0x01) — Fields: `{ user_agent, scheme, principal, credentials, realm, routing }`
- `LOGON` (0x6A) — Fields: `{ scheme, principal, credentials, realm }`
- `LOGOFF` (0x6B)
- `GOODBYE` (0x02)

**Transactional control**
- `BEGIN` (0x11) — Fields: `{ bookmarks?, tx_metadata?, mode?, db?, timeout_ms? }`
- `COMMIT` (0x12)
- `ROLLBACK` (0x13)

**Query execution**
- `RUN` (0x10) — Fields: `(query_text, parameters_map, extras_map)` where extras may include `db`, `bookmarks`, `tx_metadata`, `mode`, `timeout_ms`, `tx_timeout_ms`, `imp_user`
- `PULL` (0x3F) — Fields: `{ n?, qid? }`
- `DISCARD` (0x2F) — Fields: `{ n?, qid? }`

**Result streaming**
- `SUCCESS` (0x70) — Metadata varies by context (e.g., fields, t_first, t_last, qid, db)
- `RECORD` (0x71) — Fields: list of values in column order
- `FAILURE` (0x7F) — Fields: `{ code, message }`
- `IGNORED` (0x7E)

**Reset / attention**
- `RESET` (0x0F) — Abort in-flight work and return to READY

## Authentication
- Scheme: `basic` (Neo4j-compatible) via `HELLO`/`LOGON` credentials.
- Bearer/JWT: Accepted when routed through HTTP/GraphQL; Bolt uses the standard `basic` path for compatibility.
- In-cluster replication traffic does **not** use Bolt; it uses the internal TCP transport.

## Databases / multi-DB
- `db` in `RUN` extras map is honored.
- `:USE db` Cypher directive is supported.
- System database is not exposed via Bolt for queries; administrative commands should use the HTTP/Cypher system endpoint.

## Routing / cluster notes
- Bolt routing tables are not yet advertised; clients should connect directly to the primary for writes in HA standby mode.

## Known limits / differences
- Bolt v5.x features (patch auth, reactive streaming hints) are not implemented.
- `IMP_USER` is parsed but may be ignored depending on auth configuration.
- Routing context in `HELLO` is accepted but not used for cluster routing.

## Compatibility checklist
- Chunked framing and PackStream per spec.
- STRUCT signatures match Neo4j v4.x:
  - `0xB1 0x01` HELLO
  - `0xB1 0x10` RUN
  - `0xB1 0x3F` PULL
  - `0xB1 0x2F` DISCARD
  - `0xB1 0x11` BEGIN
  - `0xB0 0x12` COMMIT
  - `0xB0 0x13` ROLLBACK
  - `0xB0 0x0F` RESET
  - `0xB0 0x02` GOODBYE
  - `0xB1 0x6A` LOGON
  - `0xB0 0x6B` LOGOFF
  - `0xB1 0x70` SUCCESS
  - `0xB1 0x7F` FAILURE
  - `0xB1 0x7E` IGNORED
  - `0xB1 0x71` RECORD

For low-level expectations (chunk sizes, message flow states, and error codes), follow the Neo4j Bolt manual; NornicDB mirrors those behaviors for client compatibility.
