package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The hash chain is unkeyed: anyone who can edit the database file can edit a
// row and recompute every hash after it, and `acline verify` passes. A head
// seal is the chain head MACed with the approval token, a key nobody who
// writes the database is given (only its SHA-256 is stored, and that is not
// the key). Rewriting anything at or before a sealed event changes that
// event's hash, and forging a new seal for the rewritten chain needs the
// token. Checking a seal needs the token too: an HMAC has one key.
//
// What a seal cannot show: that the newest seal, and everything after it,
// was not deleted. An older seal then looks like the latest. That is what an
// anchor kept outside the database is for (`acline verify --head`, then
// `--expect-head`).

const headSealEvent = "head_sealed"

// ErrNoApprovalToken is returned when sealing is asked for on a store with no token.
var ErrNoApprovalToken = errors.New("no approval token is enabled, so there is no key to seal with: run `acline auth init` first")

var headSealRe = regexp.MustCompile(`^head (\d+):([0-9a-f]{64}) key=([0-9a-f]{12}) mac=([0-9a-f]{64})$`)

// headSealKeyID names the token a seal was made with without revealing it:
// the first 12 hex digits of the stored token hash, which is not secret.
func headSealKeyID(token string) string { return hashToken(token)[:12] }

func headSealMAC(token string, head ChainHead) string {
	m := hmac.New(sha256.New, []byte(token))
	fmt.Fprintf(m, "acline head seal v1\x00%d:%s", head.ID, head.Hash)
	return hex.EncodeToString(m.Sum(nil))
}

// SealHead records a head_sealed event binding the current chain head to the
// approval token, and returns the sealed head. It needs the valid token (from
// a person, or an agent that was given it) and a store with one enabled.
func (s *Store) SealHead(token string) (ChainHead, error) {
	if enabled, err := s.ApprovalTokenEnabled(); err != nil {
		return ChainHead{}, err
	} else if !enabled {
		return ChainHead{}, ErrNoApprovalToken
	}
	if _, err := s.authorize(token); err != nil {
		return ChainHead{}, err
	}
	return s.sealHeadWith(strings.TrimSpace(token))
}

// sealHeadWith seals with a token the caller has already checked.
func (s *Store) sealHeadWith(token string) (ChainHead, error) {
	head, ok, err := s.ChainHead()
	if err != nil {
		return ChainHead{}, err
	}
	if !ok {
		return ChainHead{}, errors.New("the audit trail has no hashed events to seal yet")
	}
	msg := fmt.Sprintf("head %s key=%s mac=%s", head, headSealKeyID(token), headSealMAC(token, head))
	if _, err := s.LogEvent(nil, nil, headSealEvent, msg); err != nil {
		return ChainHead{}, err
	}
	return head, nil
}

// HeadSealResult is what CheckHeadSeal found.
type HeadSealResult struct {
	Sealed  bool      // a seal made with the presented token exists
	Head    ChainHead // the head that seal covers
	SealID  int64     // the seal event
	Foreign int       // newer seals made with another token (a rotated one, or not a real seal)
}

// ErrHeadSealInvalid means a seal does not match the chain.
var ErrHeadSealInvalid = errors.New("head seal FAILED")

// CheckHeadSeal verifies the newest head seal made with token: the sealed
// event must still exist with the sealed hash, and the MAC must match. Run it
// with VerifyChain, which shows nothing after that event was altered either.
// Seals made with another token can't be checked; they are counted (Foreign)
// rather than trusted, so appending a seal under a made-up key can't stand in
// for the real one.
func (s *Store) CheckHeadSeal(token string) (HeadSealResult, error) {
	if enabled, err := s.ApprovalTokenEnabled(); err != nil {
		return HeadSealResult{}, err
	} else if !enabled {
		return HeadSealResult{}, ErrNoApprovalToken
	}
	if _, err := s.authorize(token); err != nil {
		return HeadSealResult{}, err
	}
	token = strings.TrimSpace(token)
	rows, err := s.DB.Query(`SELECT id, message FROM events WHERE type = ? ORDER BY id DESC`, headSealEvent)
	if err != nil {
		return HeadSealResult{}, err
	}
	// Read every seal before checking one: the store has a single connection,
	// so CheckAnchor can't run while this cursor is open.
	type seal struct {
		id  int64
		msg string
	}
	var seals []seal
	for rows.Next() {
		var sl seal
		if err := rows.Scan(&sl.id, &sl.msg); err != nil {
			rows.Close()
			return HeadSealResult{}, err
		}
		seals = append(seals, sl)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return HeadSealResult{}, err
	}
	var res HeadSealResult
	for _, sl := range seals {
		id, msg := sl.id, sl.msg
		m := headSealRe.FindStringSubmatch(msg)
		if m == nil || m[3] != headSealKeyID(token) {
			res.Foreign++
			continue
		}
		headID, _ := strconv.ParseInt(m[1], 10, 64)
		res.Sealed, res.SealID, res.Head = true, id, ChainHead{ID: headID, Hash: m[2]}
		if !hmac.Equal([]byte(m[4]), []byte(headSealMAC(token, res.Head))) {
			return res, fmt.Errorf("%w: seal #%d's MAC does not match: the trail up to event #%d was rewritten, or the seal was forged", ErrHeadSealInvalid, id, headID)
		}
		if err := s.CheckAnchor(res.Head); err != nil {
			return res, fmt.Errorf("%w: %v", ErrHeadSealInvalid, err)
		}
		return res, nil
	}
	return res, nil
}
