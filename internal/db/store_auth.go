package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("record not found")

// SetSetting stores a key/value setting inside system_settings.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO system_settings (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = CURRENT_TIMESTAMP
	`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// GetSetting fetches a setting value from system_settings, returning "" and false if absent.
func (s *Store) GetSetting(key string) (string, bool, error) {
	var val string
	err := s.db.QueryRow(`SELECT value FROM system_settings WHERE key = ?`, key).Scan(&val)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get setting %q: %w", key, err)
	}
	return val, true, nil
}

// DeleteSetting removes a setting entry by key.
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM system_settings WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete setting %q: %w", key, err)
	}
	return nil
}

// CountUsers returns the total number of registered user accounts.
func (s *Store) CountUsers() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// CreateUser inserts a new user with an already-hashed password.
func (s *Store) CreateUser(email, passwordHash, role string) (*User, error) {
	if role == "" {
		role = "admin"
	}
	now := time.Now().UTC()
	u := &User{
		ID:           uuid.NewString(),
		Email:        email,
		PasswordHash: passwordHash,
		Role:         role,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	_, err := s.db.Exec(`
		INSERT INTO users (id, email, password_hash, role, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, u.ID, u.Email, u.PasswordHash, u.Role, u.CreatedAt, u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create user %q: %w", email, err)
	}
	return u, nil
}

// GetUserByEmail looks up a user account by email.
func (s *Store) GetUserByEmail(email string) (*User, error) {
	var u User
	err := s.db.QueryRow(`
		SELECT id, email, password_hash, role, created_at, updated_at
		FROM users WHERE email = ?
	`, email).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email %q: %w", email, err)
	}
	return &u, nil
}

// GetUserByID looks up a user account by ID.
func (s *Store) GetUserByID(id string) (*User, error) {
	var u User
	err := s.db.QueryRow(`
		SELECT id, email, password_hash, role, created_at, updated_at
		FROM users WHERE id = ?
	`, id).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get user %q: %w", id, err)
	}
	return &u, nil
}

// UpdateFirstAdminPassword resets the password hash of the primary admin user (CLI recovery).
func (s *Store) UpdateFirstAdminPassword(passwordHash string) (string, error) {
	var id, email string
	err := s.db.QueryRow(`SELECT id, email FROM users ORDER BY created_at ASC LIMIT 1`).Scan(&id, &email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("find admin user: %w", err)
	}
	if _, err := s.db.Exec(`
		UPDATE users SET password_hash = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
	`, passwordHash, id); err != nil {
		return "", fmt.Errorf("update password for %s: %w", email, err)
	}
	return email, nil
}
