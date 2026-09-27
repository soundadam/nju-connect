package credential

import (
	"os"
)

type memoryStore struct {
	secret []byte
	err    error
}

func (store *memoryStore) Inspect() error {
	if store.err != nil {
		return store.err
	}
	if store.secret == nil {
		return os.ErrNotExist
	}
	return nil
}

func (store *memoryStore) Get() ([]byte, error) {
	if err := store.Inspect(); err != nil {
		return nil, err
	}
	return append([]byte(nil), store.secret...), nil
}

func (store *memoryStore) Set(secret []byte) error {
	if store.err != nil {
		return store.err
	}
	store.secret = append(store.secret[:0], secret...)
	return nil
}
