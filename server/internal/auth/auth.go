// Package auth implements multi-user authentication with bcrypt passwords,
// JWT sessions carrying the user's id and role, and middleware that gates the
// API and enforces the admin role where required.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"easydeploy/internal/store"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type ctxKey int

const principalKey ctxKey = 0

// Principal is the authenticated caller extracted from a token.
type Principal struct {
	UserID   int64
	Username string
	Role     store.Role
}

// IsAdmin reports whether the principal has the admin role.
func (p Principal) IsAdmin() bool { return p.Role == store.RoleAdmin }

// Claims is the JWT payload.
type Claims struct {
	jwt.RegisteredClaims
	UserID int64  `json:"uid"`
	Role   string `json:"role"`
}

// Users is the subset of the store the auth manager needs.
type Users interface {
	GetUserByUsername(ctx context.Context, username string) (store.User, error)
}

// Manager issues and verifies session tokens.
type Manager struct {
	users  Users
	secret []byte
	ttl    time.Duration
}

// New creates an auth manager. If jwtSecret is empty a random one is
// generated (which invalidates existing sessions on restart).
func New(users Users, jwtSecret string) (*Manager, error) {
	secret := []byte(jwtSecret)
	if len(secret) == 0 {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		secret = []byte(hex.EncodeToString(buf))
	}
	return &Manager{users: users, secret: secret, ttl: 24 * time.Hour}, nil
}

// HashPassword returns a bcrypt hash for storage.
func HashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(b), err
}

// Login validates credentials and returns a signed JWT on success.
func (m *Manager) Login(ctx context.Context, username, password string) (string, store.User, error) {
	user, err := m.users.GetUserByUsername(ctx, username)
	if err != nil {
		// Run a dummy compare to blunt user-enumeration timing differences.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidin"), []byte(password))
		return "", store.User{}, fmt.Errorf("invalid credentials")
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return "", store.User{}, fmt.Errorf("invalid credentials")
	}
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.Username,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
		UserID: user.ID,
		Role:   string(user.Role),
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	return token, user, err
}

func (m *Manager) verify(tokenStr string) (Principal, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return m.secret, nil
	})
	if err != nil {
		return Principal{}, err
	}
	return Principal{UserID: claims.UserID, Username: claims.Subject, Role: store.Role(claims.Role)}, nil
}

// Middleware rejects requests without a valid token. Tokens may arrive as
// "Authorization: Bearer <t>" or a "?token=<t>" query parameter (for
// WebSocket clients that cannot set headers).
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenStr := bearer(r.Header.Get("Authorization"))
		if tokenStr == "" {
			tokenStr = r.URL.Query().Get("token")
		}
		if tokenStr == "" {
			http.Error(w, `{"error":"missing token"}`, http.StatusUnauthorized)
			return
		}
		principal, err := m.verify(tokenStr)
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), principalKey, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin is middleware that allows only admin principals.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !Current(r.Context()).IsAdmin() {
			http.Error(w, `{"error":"admin role required"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearer(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// Current returns the authenticated principal from the request context.
func Current(ctx context.Context) Principal {
	if p, ok := ctx.Value(principalKey).(Principal); ok {
		return p
	}
	return Principal{}
}
