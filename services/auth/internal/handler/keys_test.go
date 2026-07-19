package handler

import (
	"crypto/ed25519"
	"crypto/rand"
)

func generateEd25519Key() (pub []byte, priv []byte, err error) {
	p, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return []byte(p), []byte(k), nil
}
