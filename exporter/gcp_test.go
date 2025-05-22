package exporter

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func Test_TokenProvider(t *testing.T) {
	t.Run("should return same token while calling multiple times", func(t *testing.T) {
		p := NewGCPTokenProvider()
		p.tokenFn = func(ctx context.Context) (*oauth2.Token, error) {
			return &oauth2.Token{AccessToken: "token", Expiry: time.Now().Add(1 * time.Hour)}, nil
		}

		for i := 0; i < 10; i++ {
			token, err := p.GetToken()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if token != "token" {
				t.Fatalf("expected token 'token', got '%s'", token)
			}
		}
	})

	t.Run("should return new token after previous expires", func(t *testing.T) {
		p := NewGCPTokenProvider()
		p.expiryInterval = 100 * time.Millisecond
		p.expiryDelta = 0

		calls := 0
		p.tokenFn = func(ctx context.Context) (*oauth2.Token, error) {
			if calls == 0 {
				calls++
				return &oauth2.Token{AccessToken: "token-1", Expiry: time.Now().Add(500 * time.Millisecond)}, nil
			}
			return &oauth2.Token{AccessToken: "token-2", Expiry: time.Now().Add(1 * time.Hour)}, nil
		}

		token, err := p.GetToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "token-1" {
			t.Fatalf("expected token 'token-1', got '%s'", token)
		}

		time.Sleep(600 * time.Millisecond)

		token, err = p.GetToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "token-2" {
			t.Fatalf("expected token 'token-2', got '%s'", token)
		}
	})

	t.Run("should return error if token refresh fails", func(t *testing.T) {
		p := NewGCPTokenProvider()
		p.expiryInterval = 100 * time.Millisecond
		p.expiryDelta = 0

		calls := 0
		tokenError := errors.New("retrieving new token")
		p.tokenFn = func(ctx context.Context) (*oauth2.Token, error) {
			if calls == 0 {
				calls++
				return &oauth2.Token{AccessToken: "token-1", Expiry: time.Now().Add(500 * time.Millisecond)}, nil
			}
			return nil, tokenError
		}

		token, err := p.GetToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "token-1" {
			t.Fatalf("expected token 'token-1', got '%s'", token)
		}

		time.Sleep(600 * time.Millisecond)

		_, err = p.GetToken()
		if !errors.Is(err, tokenError) {
			t.Fatalf("expected error '%v', got '%v'", tokenError, err)
		}
	})
}
