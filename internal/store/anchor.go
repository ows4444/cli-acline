package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The hash chain proves that events were not edited or removed *from the
// middle*, but its newest end is unanchored: someone with file access who
// deletes the last N events (and the rows they sealed) leaves a shorter chain
// that is still internally valid. Nothing stored in the same database can fix
// that, because it could be truncated together with the chain. Only a value
// held outside it can.
//
// So the head is exportable: a human records "<id>:<hash>" somewhere the
// database's writers cannot reach (a password manager, a commit message, a
// ticket) and later checks the store against it.

// ChainHead is the newest hashed event.
type ChainHead struct {
	ID   int64
	Hash string
}

func (h ChainHead) String() string { return fmt.Sprintf("%d:%s", h.ID, h.Hash) }

// ChainHead returns the newest event that carries a hash; ok=false if none.
func (s *Store) ChainHead() (head ChainHead, ok bool, err error) {
	err = s.DB.QueryRow(`SELECT id, hash FROM events WHERE hash IS NOT NULL AND hash != '' ORDER BY id DESC LIMIT 1`).Scan(&head.ID, &head.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ChainHead{}, false, nil
	}
	return head, err == nil, err
}

// ParseAnchor parses an "<id>:<hash>" anchor as printed by ChainHead.String.
func ParseAnchor(s string) (ChainHead, error) {
	idStr, hash, found := strings.Cut(strings.TrimSpace(s), ":")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if !found || err != nil || id <= 0 || len(hash) != 64 {
		return ChainHead{}, fmt.Errorf("invalid anchor %q: expected <event-id>:<64-hex-hash> as printed by `acline verify --head`", s)
	}
	return ChainHead{ID: id, Hash: strings.ToLower(hash)}, nil
}

// CheckAnchor confirms the store still contains the anchored event with the
// anchored hash. Combined with a valid chain (VerifyChain), that proves nothing
// up to and including that event was truncated or rewritten since the anchor
// was taken; newer events are expected and fine.
func (s *Store) CheckAnchor(a ChainHead) error {
	var got string
	err := s.DB.QueryRow(`SELECT COALESCE(hash,'') FROM events WHERE id = ?`, a.ID).Scan(&got)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("anchored event #%d no longer exists: the audit trail was truncated after the anchor was taken", a.ID)
	}
	if err != nil {
		return err
	}
	if got != a.Hash {
		return fmt.Errorf("event #%d has a different hash than the anchor: the audit trail was rewritten after the anchor was taken", a.ID)
	}
	return nil
}
