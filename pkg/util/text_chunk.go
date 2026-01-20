package util

import "strings"

// ChunkText splits text into chunks with overlap, trying to break at natural boundaries.
// Returns the original text as a single chunk if it fits within chunkSize.
//
// This is used for embedding inputs (store content and long discover queries).
func ChunkText(text string, chunkSize, overlap int) []string {
	if chunkSize <= 0 {
		return []string{text}
	}
	if overlap < 0 {
		overlap = 0
	}
	if len(text) <= chunkSize {
		return []string{text}
	}

	var chunks []string
	start := 0

	for start < len(text) {
		end := start + chunkSize
		if end > len(text) {
			end = len(text)
		}

		// If not the last chunk, try to break at natural boundary
		if end < len(text) {
			chunk := text[start:end]

			// Try paragraph break
			if idx := strings.LastIndex(chunk, "\n\n"); idx > chunkSize/2 {
				end = start + idx
			} else if idx := strings.LastIndex(chunk, ". "); idx > chunkSize/2 {
				// Try sentence break
				end = start + idx + 1
			} else if idx := strings.LastIndex(chunk, " "); idx > chunkSize/2 {
				// Try word break
				end = start + idx
			}
		}

		chunks = append(chunks, text[start:end])

		// Move start forward, accounting for overlap
		nextStart := end - overlap
		if nextStart <= start {
			nextStart = end // Prevent infinite loop
		}
		start = nextStart
	}

	return chunks
}


