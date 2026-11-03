package merkle

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash"
)

const HistoryAlgorithm = "history-sha256-v2"
const SnapshotAlgorithm = "snapshot-sha256-v2"

// HistoricalRevision is a complete revision record used in a v2 history commitment.
type HistoricalRevision struct {
	Origin      string
	PageID      int64
	Namespace   int
	Title       string
	RevisionID  int64
	Timestamp   string
	ContentHash string
}

// HistoryAccumulator is a canonical, ordered streaming commitment over all revisions
// contained in one source export. Call Add in ascending (page ID, revision ID) order.
type HistoryAccumulator struct {
	h                  hash.Hash
	previousPageID     int64
	previousRevisionID int64
	count              int64
}

func NewHistoryAccumulator() *HistoryAccumulator {
	h := sha256.New()
	_, _ = h.Write([]byte("kol_merkle/history/v2\x00"))
	return &HistoryAccumulator{h: h}
}

func writeField(h hash.Hash, s string) {
	var l [8]byte
	binary.BigEndian.PutUint64(l[:], uint64(len(s)))
	_, _ = h.Write(l[:])
	_, _ = h.Write([]byte(s))
}

func writeInt(h hash.Hash, i int64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(i))
	_, _ = h.Write(b[:])
}

func (a *HistoryAccumulator) Add(r HistoricalRevision) error {
	if r.Origin == "" || r.PageID <= 0 || r.RevisionID <= 0 || r.Timestamp == "" || len(r.ContentHash) != 64 {
		return fmt.Errorf("invalid history revision")
	}
	if _, err := hex.DecodeString(r.ContentHash); err != nil {
		return fmt.Errorf("invalid history content hash: %w", err)
	}
	if a.count > 0 && (r.PageID < a.previousPageID || (r.PageID == a.previousPageID && r.RevisionID <= a.previousRevisionID)) {
		return fmt.Errorf("noncanonical or duplicate history revision ordering")
	}
	writeField(a.h, r.Origin)
	writeInt(a.h, r.PageID)
	writeInt(a.h, int64(r.Namespace))
	writeField(a.h, r.Title)
	writeInt(a.h, r.RevisionID)
	writeField(a.h, r.Timestamp)
	writeField(a.h, r.ContentHash)
	a.previousPageID, a.previousRevisionID = r.PageID, r.RevisionID
	a.count++
	return nil
}

func (a *HistoryAccumulator) Sum() (string, int64, error) {
	if a.count == 0 {
		return "", 0, fmt.Errorf("history commitment cannot be empty")
	}
	// The final count disambiguates the commitment format and binds its extent.
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(a.count))
	h := a.h.Sum(nil)
	total := sha256.New()
	_, _ = total.Write([]byte("kol_merkle/history/final/v2\x00"))
	_, _ = total.Write(h)
	_, _ = total.Write(b[:])
	return hex.EncodeToString(total.Sum(nil)), a.count, nil
}

// SnapshotV2Root commits a page Merkle root, complete historical revision
// commitment and byte-exact source-export SHA-256 under a v2 domain.
func SnapshotV2Root(origin, pageRoot, historyRoot, exportHash string, sourceBytes int64) (string, error) {
	if origin == "" || sourceBytes <= 0 {
		return "", fmt.Errorf("empty snapshot origin or size")
	}
	for _, d := range []string{pageRoot, historyRoot, exportHash} {
		if len(d) != 64 {
			return "", fmt.Errorf("invalid v2 digest length")
		}
		if _, err := hex.DecodeString(d); err != nil {
			return "", fmt.Errorf("invalid v2 digest: %w", err)
		}
	}
	h := sha256.New()
	_, _ = h.Write([]byte("kol_merkle/snapshot/v2\x00"))
	for _, s := range []string{origin, pageRoot, historyRoot, exportHash} {
		writeField(h, s)
	}
	writeInt(h, sourceBytes)
	return hex.EncodeToString(h.Sum(nil)), nil
}
