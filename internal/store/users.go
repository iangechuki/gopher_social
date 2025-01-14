package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrDuplicateEmail    = errors.New("a user with that email address already exists")
	ErrDuplicateUsername = errors.New("a user with that username already exists")
)

type User struct {
	ID        int64    `json:"id"`
	Username  string   `json:"username"`
	Email     string   `json:"email"`
	Password  password `json:"-"`
	CreatedAt string   `json:"created_at"`
	IsActive  bool     `json:"is_active"`
	RoleID    int64    `json:"role_id"`
	Role      *Role    `json:"role"`
}
type password struct {
	text *string
	hash []byte
}

func (p *password) Set(text string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(text), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	p.text = &text
	p.hash = hash
	return nil
}
func (p *password) Compare(text string) error {
	return bcrypt.CompareHashAndPassword(p.hash, []byte(text))
}

type UserStore struct {
	db *sql.DB
}

func (s *UserStore) Create(ctx context.Context, tx *sql.Tx, user *User) error {
	query :=
		`INSERT INTO users (username,password, email,role_id)
	VALUES ($1, $2, $3, (SELECT id FROM roles WHERE name = $4))
	RETURNING id, created_at
	`
	ctx, cancel := context.WithTimeout(ctx, QueryTimeoutDuration)
	defer cancel()

	role := user.Role.Name
	if role == "" {
		role = "user"
	}
	err := s.db.QueryRowContext(
		ctx,
		query,
		user.Username,
		user.Password.hash,
		user.Email,
		role,
	).Scan(&user.ID, &user.CreatedAt)
	if err != nil {
		switch {
		case err.Error() == `pq: duplicate key value violates unique constraint "users_email_key"`:
			return ErrDuplicateEmail
		case err.Error() == `pq: duplicate key value violates unique constraint "users_username_key"`:
			return ErrDuplicateUsername
		default:
			return err
		}
	}
	return nil
}
func (s *UserStore) GetByID(ctx context.Context, userID int64) (*User, error) {
	query := `
		SELECT users.id, username, email, password, created_at, roles.*
		FROM users
		JOIN roles ON (users.role_id = roles.id)
		WHERE users.id = $1 AND is_active = true
	`

	ctx, cancel := context.WithTimeout(ctx, QueryTimeoutDuration)
	defer cancel()

	user := &User{
		Role: &Role{},
	}
	err := s.db.QueryRowContext(
		ctx,
		query,
		userID,
	).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.Password.hash,
		&user.CreatedAt,
		&user.Role.ID,
		&user.Role.Name,
		&user.Role.Level,
		&user.Role.Description,
	)
	if err != nil {
		switch err {
		case sql.ErrNoRows:
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}
	}

	return user, nil
}

func (s *UserStore) CreateAndInvite(ctx context.Context, user *User, token string, invitationExp time.Duration) error {
	return withTx(s.db, ctx, func(tx *sql.Tx) error {

		//create user
		if err := s.Create(ctx, tx, user); err != nil {
			return err
		}
		//create the user invite
		if err := s.createUserInvitation(ctx, tx, token, invitationExp, user.ID); err != nil {
			return err
		}
		return nil

	})

}
func (s *UserStore) createUserInvitation(ctx context.Context, tx *sql.Tx, token string, invitationExp time.Duration, userID int64) error {
	query := `INSERT INTO user_invitations (token,user_id,expiry) VALUES ($1, $2, $3)`
	ctx, cancel := context.WithTimeout(ctx, QueryTimeoutDuration)
	defer cancel()
	_, err := tx.ExecContext(ctx, query, token, userID, time.Now().Add(invitationExp))
	if err != nil {
		return err
	}
	return nil
}
func NewUser(username, email, rawPassword string) (*User, error) {
	pwd := password{}
	err := pwd.Set(rawPassword)
	if err != nil {
		return nil, err
	}
	return &User{
		Username: username,
		Email:    email,
		Password: pwd,
	}, nil
}

func (s *UserStore) Activate(ctx context.Context, token string) error {
	return withTx(s.db, ctx, func(tx *sql.Tx) error {
		// 1.find user that this token belngs to
		user, err := s.getUserFromInvitation(ctx, tx, token, time.Now())
		if err != nil {
			return err
		}

		//2. update user to active
		user.IsActive = true
		if err := s.update(ctx, tx, user); err != nil {
			return err
		}
		// 3. delete user invitation
		if err := s.deleteUserInvitations(ctx, tx, user.ID); err != nil {
			return err
		}

		return nil
	})
}
func (s *UserStore) getUserFromInvitation(ctx context.Context, tx *sql.Tx, token string, exp time.Time) (*User, error) {
	query := `SELECT u.id,u.username,u.email,u.created_at,u.is_active
	FROM users u
	JOIN user_invitations ui ON ui.user_id = u.id
	WHERE ui.token = $1 AND ui.expiry > $2
	`
	ctx, cancel := context.WithTimeout(ctx, QueryTimeoutDuration)
	defer cancel()
	hash := sha256.Sum256([]byte(token))
	hashToken := hex.EncodeToString(hash[:])
	user := &User{}

	err := tx.QueryRowContext(ctx, query, hashToken, exp).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.CreatedAt,
		&user.IsActive)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}
	}
	return user, nil

}
func (s *UserStore) update(ctx context.Context, tx *sql.Tx, user *User) error {
	query := `UPDATE users SET username = $1, email = $2, is_active = $3 WHERE id = $4`
	ctx, cancel := context.WithTimeout(ctx, QueryTimeoutDuration)
	defer cancel()
	_, err := tx.ExecContext(ctx, query, user.Username, user.Email, user.IsActive, user.ID)
	if err != nil {
		return err
	}
	return nil
}

func (s *UserStore) deleteUserInvitations(ctx context.Context, tx *sql.Tx, userID int64) error {
	query := `DELETE FROM user_invitations WHERE user_id = $1`
	ctx, cancel := context.WithTimeout(ctx, QueryTimeoutDuration)
	defer cancel()
	_, err := tx.ExecContext(ctx, query, userID)
	if err != nil {
		return err
	}
	return nil
}

func (s *UserStore) Delete(ctx context.Context, userID int64) error {
	return withTx(s.db, ctx, func(tx *sql.Tx) error {

		if err := s.delete(ctx, tx, userID); err != nil {
			return err
		}
		if err := s.deleteUserInvitations(ctx, tx, userID); err != nil {
			return err
		}

		return nil
	})
}
func (s *UserStore) delete(ctx context.Context, tx *sql.Tx, userID int64) error {
	query := `DELETE FROM users WHERE id = $1`
	ctx, cancel := context.WithTimeout(ctx, QueryTimeoutDuration)
	defer cancel()
	_, err := tx.ExecContext(ctx, query, userID)
	if err != nil {
		return err
	}
	return nil
}

func (s *UserStore) GetByEmail(ctx context.Context, email string) (*User, error) {
	query := `SELECT id,username,email,password,created_at FROM users WHERE email = $1 AND is_active = TRUE`
	ctx, cancel := context.WithTimeout(ctx, QueryTimeoutDuration)
	defer cancel()
	var user User
	err := s.db.QueryRowContext(ctx, query, email).Scan(&user.ID, &user.Username, &user.Email, &user.Password.hash, &user.CreatedAt)
	if err != nil {
		switch err {
		case sql.ErrNoRows:
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}
	}
	return &user, nil
}
