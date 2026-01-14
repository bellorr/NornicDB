# Bolt / PackStream Compatibility (NornicDB)

**What this document answers**
- Which PackStream value types NornicDB can send/receive over Bolt
- Which Bolt “structures” (Node/Relationship/Path…) NornicDB emits
- Which value types are allowed in node/relationship **properties** (the `{...}` maps in Cypher)

**References**
- PackStream (value types): https://neo4j.com/docs/bolt/current/packstream/
- Bolt structure semantics (Node/Relationship/Path…): https://neo4j.com/docs/bolt/current/bolt/structure-semantics/

NornicDB speaks the Neo4j Bolt binary protocol for driver compatibility. We target Bolt v4.x semantics with backward-compatible handling of v3. Message framing, chunking, and PackStream encodings follow the Neo4j spec.

## PackStream value types (what can be encoded)

These are the PackStream “building blocks” that appear in:
- parameters you send to Cypher over Bolt
- values returned in `RECORD` fields
- node/relationship `properties` maps inside Node/Relationship structures

NornicDB supports:
- `null`
- `boolean`
- `integer` (encoded as signed 64-bit; smaller ints are widened)
- `float` (IEEE-754 double on the wire)
- `string`
- `bytes`
- `list` (arrays)
- `map` (key/value; string keys)
- `struct` (used for Node/Relationship/Path and other typed values)

## Bolt structures NornicDB emits

NornicDB emits the standard Neo4j driver-facing graph structures:
- **Node** (`N`, signature `0x4E`): `[id:int, labels:list<string>, properties:map]`
- **Relationship** (`R`, signature `0x52`): `[id:int, start:int, end:int, type:string, properties:map]`
- **Path** (`P`, signature `0x50`): `[nodes:list<Node>, rels:list<UnboundRel>, sequence:list<int>]` (when returned by queries)

### ID encoding note (Neo4j driver compatibility)
Internally NornicDB uses string IDs. Bolt expects integer IDs for Node/Relationship structures, so NornicDB deterministically hashes string IDs to a positive `int64` for the Node/Relationship `id` fields.

If you need the original stable ID, use Cypher functions like:
- `id(n)` (returns NornicDB string ID)
- `elementId(n)` (Neo4j-style string form, e.g. `4:nornicdb:<id>`)

## Property value types (what you can store in `{ ... }`)

This is the key compatibility question: **what types are allowed as node/relationship properties?**

### NornicDB (this project)
NornicDB allows **any PackStream-encodable value** as a property value, including:
- primitives: `null`, `boolean`, `integer`, `float`, `string`, `bytes`
- lists/arrays of the above (including mixed-type lists)
- **nested maps/objects** (`map<string, any>`) and combinations of nested maps + lists

This is closer to “document DB” semantics for properties and is more permissive than Neo4j.

Full reference with examples: `docs/user-guides/property-data-types.md`

### Neo4j (for comparison)
Neo4j historically restricts property values to primitives + arrays of primitives (no nested maps as property values). Bolt/PackStream itself can represent maps anywhere, but Neo4j won’t store maps as properties.

If you are aiming for strict Neo4j property semantics (maximum portability), avoid nested maps and prefer flattening or modeling via nodes/relationships.

### Important edge case: reserved map shape
Over Bolt, NornicDB has a small convenience encoding that treats a map with keys:
- `_nodeId`
- `labels`
as a Node-like value.

If you store a property whose value is a map with both of those keys, it may be encoded as a Node structure to the driver instead of a plain map. If you need to store arbitrary nested maps safely, avoid using `_nodeId`+`labels` together at the same nesting level.

## Supported Bolt messages (transport-level)

This is the “message layer” (handshake/query/streaming), included here for completeness:

**Handshake**
- `INIT` / `HELLO` (0x01)
- `LOGON` (0x6A)
- `LOGOFF` (0x6B)
- `GOODBYE` (0x02)

**Transactional control**
- `BEGIN` (0x11)
- `COMMIT` (0x12)
- `ROLLBACK` (0x13)

**Query execution**
- `RUN` (0x10)
- `PULL` (0x3F)
- `DISCARD` (0x2F)

**Result streaming**
- `SUCCESS` (0x70)
- `RECORD` (0x71)
- `FAILURE` (0x7F)
- `IGNORED` (0x7E)

**Reset / attention**
- `RESET` (0x0F)

## Authentication
- Bolt uses Neo4j-compatible `basic` auth via `HELLO`/`LOGON`.
- HTTP/GraphQL can use JWT/cookies; replication traffic uses the internal cluster transport (not Bolt).

## Databases / multi-DB
- `db` in `RUN` extras map is honored.
- `:USE <db>` Cypher directive is supported.

## Node ID Handling

NornicDB uses string IDs (UUIDs) internally, but Bolt protocol returns nodes with integer IDs for Neo4j compatibility. The integer ID is a deterministic hash of the string ID using FNV-1a algorithm.

**Using integer IDs in queries:**

You can use the integer ID from a Bolt Node structure directly in Cypher queries:

```cypher
// Get node from Bolt (returns integer ID in Node structure)
// node.id = 1234567890123456789

// Use integer ID in WHERE clause - works automatically
MATCH (n) WHERE id(n) = 1234567890123456789 RETURN n

// String IDs also work (original behavior)
MATCH (n) WHERE id(n) = 'db846409-03af-45a7-9e0f-b21cf1842ae2' RETURN n
```

The `id()` function automatically detects whether you're comparing with an integer (from Bolt) or a string (native NornicDB ID) and handles both cases correctly.

**Note:** The hash is one-way - you cannot convert an integer ID back to the original string ID. Use `elementId()` to get the full string ID format (`4:nornicdb:uuid`).

## Known limits / differences
- Neo4j native temporal/spatial types are not currently encoded as Bolt temporal structs; `time.Time` is currently encoded as an integer (Unix millis) for a stable scalar representation.
- Bolt routing tables are not yet advertised; in HA standby, connect writes to the primary.
