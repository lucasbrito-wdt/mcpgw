package admin

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/store"
)

type User struct {
	ID, Email string
	IsAdmin   bool
	CreatedAt time.Time
}

type CreateUserInput struct {
	Email, Password string
	IsAdmin         bool
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.o.Pool.Query(ctx, `SELECT id::text, email, is_admin, created_at FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (u User, err error) {
		err = r.Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt)
		return
	})
}

func hashPassword(password string) (string, error) {
	hash, err := oauth.HashPassword(password)
	if errors.Is(err, oauth.ErrWeakPassword) {
		return "", invalid("password", "weak password: use 12 to 1024 characters", "senha fraca: use entre 12 e 1024 caracteres")
	}
	return hash, err
}

func (s *Service) CreateUser(ctx context.Context, in CreateUserInput) (User, error) {
	email, err := NormalizeEmail(in.Email)
	if err != nil {
		return User{}, err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = s.write(ctx, Event{Action: "user.create", Target: email, Details: map[string]any{"is_admin": in.IsAdmin}}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `INSERT INTO users (email, password_hash, is_admin) VALUES ($1, $2, $3)
			RETURNING id::text, email, is_admin, created_at`, email, hash, in.IsAdmin).Scan(&u.ID, &u.Email, &u.IsAdmin, &u.CreatedAt)
	})
	if isUniqueViolation(err) {
		return User{}, conflict("a user with email %s already exists", "já existe um usuário com o e-mail %s", email)
	}
	return u, err
}

func (s *Service) SetAdmin(ctx context.Context, userID string, isAdmin bool) error {
	if !validUUID(userID) {
		return notFound("user not found: %s", "usuário não encontrado: %s", userID)
	}
	return s.write(ctx, Event{Action: "user.set_admin", Target: userID, Details: map[string]any{"is_admin": isAdmin}}, func(tx pgx.Tx) error {
		// Locking every admin row serializes concurrent demotions.
		rows, err := tx.Query(ctx, `SELECT id::text FROM users WHERE is_admin FOR UPDATE`)
		if err != nil {
			return err
		}
		admins, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if !isAdmin && len(admins) == 1 && strings.EqualFold(admins[0], userID) {
			return conflict("cannot remove the last admin", "não é possível remover o último admin")
		}
		tag, err := tx.Exec(ctx, `UPDATE users SET is_admin = $2 WHERE id = $1::uuid`, userID, isAdmin)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return notFound("user not found: %s", "usuário não encontrado: %s", userID)
		}
		return nil
	})
}

// ResetPassword never records the password in the audit details.
func (s *Service) ResetPassword(ctx context.Context, userID, password string) error {
	if !validUUID(userID) {
		return notFound("user not found: %s", "usuário não encontrado: %s", userID)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	return s.write(ctx, Event{Action: "user.reset_password", Target: userID, Details: map[string]any{}}, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1::uuid`, userID, hash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return notFound("user not found: %s", "usuário não encontrado: %s", userID)
		}
		return nil
	})
}

func (s *Service) UserIDByEmail(ctx context.Context, email string) (string, error) {
	return userIDByEmail(ctx, s.o.Pool, email)
}

func userIDByEmail(ctx context.Context, db store.DB, rawEmail string) (string, error) {
	email, err := NormalizeEmail(rawEmail)
	if err != nil {
		return "", err
	}
	var id string
	err = db.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", notFound("user not found: %s", "usuário não encontrado: %s", email)
	}
	return id, err
}
