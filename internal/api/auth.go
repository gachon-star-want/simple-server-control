package api

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/db"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookieName = "ssc_session"

type ctxKey string

const userCtxKey ctxKey = "ssc_user"

type authClaims struct {
	UserID string `json:"uid"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	jwt.RegisteredClaims
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.CountUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	user, _ := s.authenticateRequest(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"needs_setup":   count == 0,
		"authenticated": user != nil,
		"user":          user,
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	count, err := s.store.CountUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DB_ERROR", err.Error())
		return
	}
	if count > 0 {
		writeError(w, http.StatusForbidden, "SETUP_COMPLETED", "초기 관리자 계정이 이미 설정되었습니다.")
		return
	}

	var req struct {
		SetupToken string `json:"setup_token"`
		Email      string `json:"email"`
		Password   string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "요청 본문 형식이 올바르지 않습니다.")
		return
	}

	providedTok := strings.TrimSpace(req.SetupToken)
	if s.cfg.SetupToken == "" || !hmac.Equal([]byte(providedTok), []byte(s.cfg.SetupToken)) {
		writeError(w, http.StatusForbidden, "INVALID_SETUP_TOKEN", "Setup Token이 일치하지 않습니다. 서버 터미널의 setup.token 값을 확인해 주세요.")
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !strings.Contains(email, "@") || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "INVALID_INPUT", "유효한 이메일 주소와 8자 이상의 비밀번호를 입력해 주세요.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "HASH_ERROR", err.Error())
		return
	}
	user, err := s.store.CreateUser(email, string(hash), "admin")
	if err != nil {
		writeError(w, http.StatusBadRequest, "USER_CREATE_FAILED", err.Error())
		return
	}

	if err := s.setSessionCookie(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, "JWT_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "잘못된 요청 형식입니다.")
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	user, err := s.store.GetUserByEmail(email)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "이메일 또는 비밀번호가 올바르지 않습니다.")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "이메일 또는 비밀번호가 올바르지 않습니다.")
		return
	}

	if err := s.setSessionCookie(w, r, user); err != nil {
		writeError(w, http.StatusInternalServerError, "JWT_ERROR", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPSRequest(r),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.authenticateRequest(r)
		if err != nil || user == nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "로그인이 필요합니다.")
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) authenticateRequest(r *http.Request) (*db.User, error) {
	var rawToken string
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		rawToken = c.Value
	} else if authz := r.Header.Get("Authorization"); strings.HasPrefix(authz, "Bearer ") {
		rawToken = strings.TrimPrefix(authz, "Bearer ")
	}
	if rawToken == "" {
		return nil, errors.New("missing session token")
	}

	claims := &authClaims{}
	parsed, err := jwt.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(s.cfg.JWTSecret), nil
	})
	if err != nil || !parsed.Valid {
		return nil, errors.New("invalid session token")
	}
	return s.store.GetUserByID(claims.UserID)
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, user *db.User) error {
	now := time.Now()
	claims := authClaims{
		UserID: user.ID,
		Email:  user.Email,
		Role:   user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(7 * 24 * time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(s.cfg.JWTSecret))
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    signed,
		Path:     "/",
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode, // Required so GET redirects returning from GitHub preserve session.
		Secure:   isHTTPSRequest(r),
	})
	return nil
}

func currentUser(r *http.Request) *db.User {
	if u, ok := r.Context().Value(userCtxKey).(*db.User); ok {
		return u
	}
	return nil
}

func isHTTPSRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
