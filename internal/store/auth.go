package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// The approval token exists because "an agent cannot approve its own work" was
// only ever a check on ACLINE_ACTOR_TYPE -- an environment variable the agent
// controls. Once a human enables the token, the privileged actions (approving a
// task, overriding the gate with --force, approving agent-written memory) also
// require proof of a secret the agent is never given.
//
// It is opt-in: with no token configured every check below is a no-op and
// behavior is exactly what it was before.
//
// Only a SHA-256 hash is stored. The token is 256 bits of randomness, so a
// plain hash (no work factor) is sufficient -- there is nothing to brute-force.
// The hash lives in `meta`, which snapshots deliberately do not carry, so an
// import cannot reset or replace the credential.
const metaApprovalTokenHash = "approval_token_hash"

var (
	// ErrApprovalTokenRequired means a token is configured and none was supplied.
	ErrApprovalTokenRequired = errors.New("an approval token is required (this store has one enabled): supply ACLINE_APPROVAL_TOKEN, or run this from an interactive terminal")
	// ErrApprovalTokenInvalid means a token was supplied but doesn't match.
	ErrApprovalTokenInvalid = errors.New("invalid approval token")
	// ErrApprovalTokenAlreadyEnabled is returned by EnableApprovalToken.
	ErrApprovalTokenAlreadyEnabled = errors.New("an approval token is already enabled; rotate or disable it with the current token")
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) approvalTokenHash() (string, error) {
	var h string
	err := s.DB.QueryRow(`SELECT value FROM meta WHERE key = ?`, metaApprovalTokenHash).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return h, err
}

// ApprovalTokenEnabled reports whether privileged actions require the token.
func (s *Store) ApprovalTokenEnabled() (bool, error) {
	h, err := s.approvalTokenHash()
	return h != "", err
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating token: %w", err)
	}
	return "acl_" + hex.EncodeToString(b), nil
}

// EnableApprovalToken generates a token, stores its hash, and returns the
// token -- the only time it exists in cleartext. It refuses if one is already
// enabled (use RotateApprovalToken, which needs the current token).
func (s *Store) EnableApprovalToken() (string, error) {
	if enabled, err := s.ApprovalTokenEnabled(); err != nil {
		return "", err
	} else if enabled {
		return "", ErrApprovalTokenAlreadyEnabled
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	// INSERT OR IGNORE + a re-check guards two racing enables: only one wins.
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO meta (key, value) VALUES (?, ?)`, metaApprovalTokenHash, hashToken(token))
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrApprovalTokenAlreadyEnabled
	}
	s.LogEventGlobal("approval_token", "approval token enabled")
	return token, nil
}

// CheckApprovalToken validates token against the stored hash. When no token is
// enabled it returns nil for any input -- the feature is off.
func (s *Store) CheckApprovalToken(token string) error {
	_, err := s.authorize(token)
	return err
}

// authorize reports whether the caller proved knowledge of the token
// (viaToken). With no token enabled it returns (false, nil): nothing was
// proven, nothing is demanded, and the caller applies the legacy rules.
func (s *Store) authorize(token string) (viaToken bool, err error) {
	want, err := s.approvalTokenHash()
	if err != nil {
		return false, err
	}
	if want == "" {
		return false, nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false, ErrApprovalTokenRequired
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(want)) != 1 {
		s.LogEventGlobal("approval_token", "approval token check FAILED")
		return false, ErrApprovalTokenInvalid
	}
	return true, nil
}

// requirePerson refuses an agent actor unless it presents the approval token,
// and enforces the token whenever one is enabled. errAgent is what a refused
// agent sees. It is the check behind every privileged action (see
// docs/ARCHITECTURE.md, "Adding a privileged action").
func (s *Store) requirePerson(token string, errAgent error) error {
	viaToken, err := s.authorize(token)
	if err != nil {
		return err
	}
	if s.Actor.Type == "agent" && !viaToken {
		return errAgent
	}
	return nil
}

// RotateApprovalToken replaces the token; the current one is required.
func (s *Store) RotateApprovalToken(current string) (string, error) {
	if enabled, err := s.ApprovalTokenEnabled(); err != nil {
		return "", err
	} else if !enabled {
		return "", errors.New("no approval token is enabled")
	}
	if _, err := s.authorize(current); err != nil {
		return "", err
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}
	if _, err := s.DB.Exec(`UPDATE meta SET value = ? WHERE key = ?`, hashToken(token), metaApprovalTokenHash); err != nil {
		return "", err
	}
	s.LogEventGlobal("approval_token", "approval token rotated")
	// Seals made with the old token can no longer be checked; seal the head
	// with the new one so the newest seal always can be. Best effort: the
	// rotation itself has already happened.
	_, _ = s.sealHeadWith(token)
	return token, nil
}

// DisableApprovalToken turns the requirement off; the current token is required.
func (s *Store) DisableApprovalToken(current string) error {
	if enabled, err := s.ApprovalTokenEnabled(); err != nil {
		return err
	} else if !enabled {
		return errors.New("no approval token is enabled")
	}
	if _, err := s.authorize(current); err != nil {
		return err
	}
	if _, err := s.DB.Exec(`DELETE FROM meta WHERE key = ?`, metaApprovalTokenHash); err != nil {
		return err
	}
	s.LogEventGlobal("approval_token", "approval token disabled")
	return nil
}
