package util

import (
	"golang.org/x/crypto/sha3"
	"crypto/sha256"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

type Hash []byte

func (h Hash) Hex() string {
	return hexutil.Encode(h)
}

type Hasher func([]byte) Hash

type Hashable interface {
	Hash(Hasher) Hash
}

type RLPHashable interface {
	RLPHash(Hasher) Hash
}

func Keccak256(b []byte) Hash {
	hash := sha3.NewLegacyKeccak256()

	var buf []byte
	hash.Write(b)
	buf = hash.Sum(buf)

	return buf
}

func Sha256(b []byte) Hash {
	hash := sha256.Sum256(b)
	return hash[:]
}

func GethHash(b []byte) Hash {
	hasher := sha3.NewLegacyKeccak256()
	hasher.Write([]byte("\x19Ethereum Signed Message:\n32"))
	hasher.Write(b)
	return hasher.Sum(nil)
}