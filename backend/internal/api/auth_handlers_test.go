package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"easygpa/backend/internal/auth"
	"easygpa/backend/internal/config"
	"easygpa/backend/internal/notify"
)

type refreshTestMailer struct{}

func (refreshTestMailer) Send(context.Context, notify.Message) (string, error) {
	panic("refresh must not send mail")
}

func TestRefreshOnlyClearsCookieForInvalidCredential(t *testing.T) {
	// An unconnected pool and a closed Redis client exercise the real auth
	// service's error path without contacting any database or external service.
	pool, err := pgxpool.New(t.Context(), "postgres://test:test@127.0.0.1:1/test?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 32)
	tokens, err := auth.NewTokenManager(secret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := auth.NewSessionStore(client, time.Hour, secret)
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.NewService(pool, client, tokens, sessions, refreshTestMailer{}, auth.ServiceConfig{
		OpsAccount: "ops@example.org", OpsPassword: "example-test-password-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{cfg: &config.Config{}, deps: Dependencies{Auth: service}}
	for _, test := range []struct {
		name   string
		token  string
		status int
		clear  bool
	}{
		{name: "invalid credential", token: "invalid", status: http.StatusUnauthorized, clear: true},
		{name: "Redis unavailable", token: strings.Repeat("a", 32) + "." + strings.Repeat("b", 43), status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
			c.Request.AddCookie(&http.Cookie{Name: refreshCookieName, Value: test.token})
			server.refresh(c)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body.String())
			}
			cleared := false
			for _, cookie := range recorder.Result().Cookies() {
				if cookie.Name == refreshCookieName && cookie.MaxAge < 0 {
					cleared = true
				}
			}
			if cleared != test.clear {
				t.Fatalf("cleared refresh cookie = %t, want %t", cleared, test.clear)
			}
		})
	}
}
