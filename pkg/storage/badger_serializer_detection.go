package storage

import (
	"fmt"

	"github.com/dgraph-io/badger/v4"
)

func detectStoredSerializer(db *badger.DB) (StorageSerializer, bool, error) {
	if db == nil {
		return "", false, fmt.Errorf("nil badger db")
	}

	var detected StorageSerializer
	err := db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.PrefetchValues = true

		prefixes := [][]byte{
			{prefixNode},
			{prefixEdge},
			{prefixEmbedding},
		}

		for _, prefix := range prefixes {
			opts.Prefix = prefix
			it := txn.NewIterator(opts)
			for it.Rewind(); it.ValidForPrefix(prefix); it.Next() {
				item := it.Item()
				val, err := item.ValueCopy(nil)
				if err != nil {
					it.Close()
					return err
				}
				if len(val) == 0 {
					continue
				}
				serializer, _, ok, err := splitSerializationHeader(val)
				if err != nil {
					it.Close()
					return err
				}
				if ok {
					detected = serializer
				} else {
					detected = StorageSerializerGob
				}
				it.Close()
				return ErrIterationStopped
			}
			it.Close()
		}
		return nil
	})
	if err == ErrIterationStopped {
		err = nil
	}
	if err != nil {
		return "", false, err
	}
	if detected == "" {
		return "", false, nil
	}
	return detected, true, nil
}
