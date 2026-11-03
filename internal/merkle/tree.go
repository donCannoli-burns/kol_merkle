// Package merkle defines the versioned, deterministic snapshot hash contract.
package merkle

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
)

const leafDomain = "kol_merkle/leaf/v1\x00"
const nodeDomain = "kol_merkle/node/v1\x00"
const emptyDomain = "kol_merkle/empty/v1\x00"

// Entry identifies the exact imported revision and its original content bytes.
type Entry struct {
	Origin      string
	PageID      int64
	Namespace   int
	Title       string
	RevisionID  int64
	ContentHash string
}

func key(e Entry) []byte {
	var b bytes.Buffer
	for _, s := range []string{e.Origin, e.Title, e.ContentHash} {
		_ = binary.Write(&b, binary.BigEndian, uint64(len(s)))
		b.WriteString(s)
	}
	_ = binary.Write(&b, binary.BigEndian, e.PageID)
	_ = binary.Write(&b, binary.BigEndian, int64(e.Namespace))
	_ = binary.Write(&b, binary.BigEndian, e.RevisionID)
	return b.Bytes()
}

// Root computes a binary Merkle root over canonically sorted leaf records.
// Odd levels duplicate the final hash. This format must be versioned if changed.
func Root(entries []Entry) (string, error) {
	leaves := make([][]byte, 0, len(entries))
	for _, e := range entries {
		if e.Origin == "" || e.PageID <= 0 || e.RevisionID <= 0 || len(e.ContentHash) != 64 {
			return "", fmt.Errorf("invalid Merkle entry %+v", e)
		}
		if _, err := hex.DecodeString(e.ContentHash); err != nil {
			return "", fmt.Errorf("invalid content hash: %w", err)
		}
		leaves = append(leaves, key(e))
	}
	sort.Slice(leaves, func(i, j int) bool { return bytes.Compare(leaves[i], leaves[j]) < 0 })
	if len(leaves) == 0 {
		sum := sha256.Sum256([]byte(emptyDomain))
		return hex.EncodeToString(sum[:]), nil
	}
	hashes := make([][32]byte, len(leaves))
	for i, v := range leaves {
		hashes[i] = sha256.Sum256(append([]byte(leafDomain), v...))
		if i > 0 && bytes.Equal(leaves[i-1], v) {
			return "", fmt.Errorf("duplicate snapshot entry")
		}
	}
	for len(hashes) > 1 {
		next := make([][32]byte, 0, (len(hashes)+1)/2)
		for i := 0; i < len(hashes); i += 2 {
			right := hashes[i]
			if i+1 < len(hashes) {
				right = hashes[i+1]
			}
			buf := make([]byte, 0, len(nodeDomain)+64)
			buf = append(buf, []byte(nodeDomain)...)
			buf = append(buf, hashes[i][:]...)
			buf = append(buf, right[:]...)
			next = append(next, sha256.Sum256(buf))
		}
		hashes = next
	}
	return hex.EncodeToString(hashes[0][:]), nil
}
