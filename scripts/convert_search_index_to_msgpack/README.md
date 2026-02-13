# Convert search index files from gob to msgpack

Search index persistence now uses **msgpack** only and **extensionless** paths (`bm25`, `vectors`, `hnsw`, `hnsw_ivf/0`, `1`, …). This script converts existing `.gob` files to msgpack, writes to the new paths, and removes the old `.gob` files.

## Steps

1. **Back up** the search index directory (e.g. `cp -r data/test/search data/test/search.bak`).
2. **Stop NornicDB** so files aren't being written.
3. **Run the script:**
   ```bash
   go run ./scripts/convert_search_index_to_msgpack --dir data/test/search
   ```
4. **Restart NornicDB.** It will load the msgpack files; no index rebuild needed.

## What gets converted

| Old path       | New path   |
|----------------|------------|
| `bm25.gob`     | `bm25`     |
| `vectors.gob`  | `vectors`  |
| `hnsw.gob`     | `hnsw`     |
| `hnsw_ivf/N.gob` | `hnsw_ivf/N` |

After a successful convert, the script deletes each `.gob` file. If the new path already exists and is valid msgpack, that file is skipped (idempotent).

Default `--dir` is `data/test/search`. Use the path that contains your database subdirs (e.g. `nornic`, `translations`).

## Large indexes

Converting a very large `vectors.gob` (e.g. 3 GB) can take a minute or two. The script reads gob, writes msgpack to the new path, then removes the `.gob` file.
