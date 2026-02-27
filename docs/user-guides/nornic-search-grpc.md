# NornicSearch gRPC With Existing Qdrant Drivers

Use existing Qdrant gRPC drivers unchanged, and add NornicDB's `NornicSearch` proto as an extra client when you want `SearchText`.

You do **not** need to fork Qdrant drivers.

## When You Need This

- You already use Qdrant gRPC APIs (`Points.Search`, `Points.Query`, etc.).
- You also want Nornic-native `NornicSearch/SearchText`.
- You want both over the same endpoint (`:6334` by default).

## Prerequisites

- `NORNICDB_QDRANT_GRPC_ENABLED=true`
- Port `6334` exposed/reachable
- Existing Qdrant gRPC client in your app (unchanged)
- Add generated client code from `pkg/nornicgrpc/proto/nornicdb_search.proto`

## Architecture (Side-by-Side Clients)

- **Qdrant client**: talks to Qdrant-compatible services
- **Nornic client**: talks to `NornicSearch` service
- **Same gRPC channel**: both clients can share one connection to `127.0.0.1:6334`

## Go Example (Recommended)

1) Keep your current Qdrant client/protos.

2) Generate Go stubs for Nornic proto:

```bash
protoc \
  --go_out=. \
  --go-grpc_out=. \
  pkg/nornicgrpc/proto/nornicdb_search.proto
```

3) Create both clients on the same connection:

```go
conn, err := grpc.Dial("127.0.0.1:6334", grpc.WithTransportCredentials(insecure.NewCredentials()))
if err != nil {
    log.Fatal(err)
}
defer conn.Close()

qdrantClient := qdrant.NewPointsClient(conn)             // Existing Qdrant driver usage
nornicClient := nornicpb.NewNornicSearchClient(conn)     // Additive Nornic client

// Existing Qdrant-compatible vector search
_, err = qdrantClient.Search(ctx, &qdrant.SearchPoints{
    CollectionName: "my_vectors",
    Vector:         make([]float32, 128),
    Limit:          10,
})
if err != nil {
    log.Fatal(err)
}

// Nornic-native text search
resp, err := nornicClient.SearchText(ctx, &nornicpb.SearchTextRequest{
    Query: "machine learning",
    Limit: 10,
})
if err != nil {
    log.Fatal(err)
}
_ = resp
```

## Existing Qdrant Text Query (No Nornic Proto Needed)

If you only need text query with existing Qdrant drivers, use `Points.Query` with `Document.text`:

```python
from qdrant_client import QdrantClient, models

client = QdrantClient(host="127.0.0.1", grpc_port=6334, prefer_grpc=True)
client.query_points(
    collection_name="my_vectors",
    query=models.Document(text="machine learning"),
    limit=10,
)
```

## Which Path Should I Use?

- Use **Qdrant `Points.Query` + `Document.text`** when you want pure Qdrant-driver semantics.
- Use **`NornicSearch/SearchText`** when you want Nornic-native typed search RPC behavior.
- Use both together if your app needs both compatibility and native features.
