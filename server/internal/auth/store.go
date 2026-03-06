package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"golang.org/x/crypto/argon2"
)

type User struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

type UserStore struct {
	db *sql.DB
}

func NewUserStore(db *sql.DB) *UserStore {
	return &UserStore{db: db}
}

func (s *UserStore) Create(username, password string) (*User, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	result, err := s.db.Exec(
		"INSERT INTO users (username, password) VALUES (?, ?)",
		username, hash,
	)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get last insert id: %w", err)
	}
	return &User{ID: id, Username: username, CreatedAt: time.Now()}, nil
}

func (s *UserStore) Authenticate(username, password string) (*User, error) {
	var user User
	var hash string
	err := s.db.QueryRow(
		"SELECT id, username, password, created_at FROM users WHERE username = ?",
		username,
	).Scan(&user.ID, &user.Username, &hash, &user.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invalid credentials")
	}
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}

	if !verifyPassword(password, hash) {
		return nil, fmt.Errorf("invalid credentials")
	}

	return &user, nil
}

func (s *UserStore) GetByID(id int64) (*User, error) {
	var user User
	err := s.db.QueryRow(
		"SELECT id, username, created_at FROM users WHERE id = ?",
		id,
	).Scan(&user.ID, &user.Username, &user.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query user: %w", err)
	}
	return &user, nil
}

// Password hashing using Argon2id
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	// Argon2id parameters: time=1, memory=64MB, threads=4, keyLen=32
	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)

	// Encode as salt:hash in hex
	return hex.EncodeToString(salt) + ":" + hex.EncodeToString(hash), nil
}

func verifyPassword(password, encoded string) bool {
	parts := splitOnce(encoded, ':')
	if parts == nil {
		return false
	}

	salt, err := hex.DecodeString(parts[0])
	if err != nil {
		return false
	}

	expectedHash, err := hex.DecodeString(parts[1])
	if err != nil {
		return false
	}

	hash := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)

	if len(hash) != len(expectedHash) {
		return false
	}
	// Constant-time comparison
	var diff byte
	for i := range hash {
		diff |= hash[i] ^ expectedHash[i]
	}
	return diff == 0
}

func splitOnce(s string, sep byte) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return []string{s[:i], s[i+1:]}
		}
	}
	return nil
}

// Session management

type Session struct {
	ID          string
	UserID      int64
	RefreshHash string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

type SessionStore struct {
	db *sql.DB
}

func NewSessionStore(db *sql.DB) *SessionStore {
	return &SessionStore{db: db}
}

const maxSessionsPerUser = 10

func (s *SessionStore) Create(userID int64) (sessionID, refreshToken string, err error) {
	// Enforce session limit: delete oldest sessions beyond the limit
	_, _ = s.db.Exec(`
		DELETE FROM sessions WHERE id IN (
			SELECT id FROM sessions WHERE user_id = ?
			ORDER BY created_at DESC
			LIMIT -1 OFFSET ?
		)
	`, userID, maxSessionsPerUser-1)

	sessionID = generateID()
	refreshToken = generateID() + generateID()

	h := sha256.Sum256([]byte(refreshToken))
	refreshHash := hex.EncodeToString(h[:])

	expiresAt := time.Now().Add(RefreshTokenDuration)

	_, err = s.db.Exec(
		"INSERT INTO sessions (id, user_id, refresh_hash, expires_at) VALUES (?, ?, ?, ?)",
		sessionID, userID, refreshHash, expiresAt,
	)
	if err != nil {
		return "", "", fmt.Errorf("insert session: %w", err)
	}

	return sessionID, refreshToken, nil
}

func (s *SessionStore) ValidateRefresh(refreshToken string) (*Session, error) {
	h := sha256.Sum256([]byte(refreshToken))
	refreshHash := hex.EncodeToString(h[:])

	var session Session
	err := s.db.QueryRow(
		"SELECT id, user_id, refresh_hash, expires_at, created_at FROM sessions WHERE refresh_hash = ?",
		refreshHash,
	).Scan(&session.ID, &session.UserID, &session.RefreshHash, &session.ExpiresAt, &session.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invalid refresh token")
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}

	if time.Now().After(session.ExpiresAt) {
		_, _ = s.db.Exec("DELETE FROM sessions WHERE id = ?", session.ID)
		return nil, fmt.Errorf("refresh token expired")
	}

	return &session, nil
}

func (s *SessionStore) Delete(sessionID string) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", sessionID)
	return err
}

func (s *SessionStore) DeleteAllForUser(userID int64) error {
	_, err := s.db.Exec("DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}

func NewID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func generateID() string {
	return NewID()
}
