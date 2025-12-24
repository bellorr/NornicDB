package storage

import "encoding/binary"

// buildAtomicRecordV2 builds a single WAL record in the v2 atomic format:
//
//	[magic:4][version:1][length:4][payload:N][crc:4][trailer:8][padding:0-7]
//
// The record is 8-byte aligned to prevent torn headers and to make padding skips deterministic.
// The returned int64 is the aligned record length (for accounting/stats).
func buildAtomicRecordV2(payload []byte) ([]byte, int64) {
	entryCRC := crc32Checksum(payload)

	headerSize := int64(4 + 1 + 4)          // magic + version + length
	bodySize := int64(len(payload) + 4 + 8) // payload + crc + trailer
	rawRecordLen := headerSize + bodySize
	alignedRecordLen := alignUp(rawRecordLen)

	record := make([]byte, alignedRecordLen)
	offset := 0

	binary.LittleEndian.PutUint32(record[offset:], walMagic)
	offset += 4
	record[offset] = walFormatVersion
	offset++
	binary.LittleEndian.PutUint32(record[offset:], uint32(len(payload)))
	offset += 4
	copy(record[offset:], payload)
	offset += len(payload)
	binary.LittleEndian.PutUint32(record[offset:], entryCRC)
	offset += 4
	binary.LittleEndian.PutUint64(record[offset:], walTrailer)
	// padding is already zeroed by make(...)

	return record, alignedRecordLen
}
