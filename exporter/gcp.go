package exporter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	// expiryDelta is a duration token is considered expired before its actual expiration time.
	expiryDelta = 60 * time.Second
)

var (
	ErrTokenExpired = errors.New("token expired")
)

type GCPTokenProvider struct {
	mu             sync.RWMutex
	expiryInterval time.Duration
	expiryDelta    time.Duration

	tokenFn  func(ctx context.Context) (*oauth2.Token, error)
	tokenErr error
	token    *oauth2.Token
}

func NewGCPTokenProvider() *GCPTokenProvider {
	return &GCPTokenProvider{
		mu:          sync.RWMutex{},
		tokenFn:     getToken,
		expiryDelta: expiryDelta,
	}
}

// GetToken returns access token that can be used to authorize
// the requests to access google services
func (p *GCPTokenProvider) GetToken() (string, error) {
	if p.token == nil || p.expired() {
		p.mu.Lock()
		p.token, p.tokenErr = p.tokenFn(context.Background())
		p.mu.Unlock()
	}

	if p.tokenErr != nil {
		return "", p.tokenErr
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if p.token.Expiry.Before(time.Now()) {
		return "", fmt.Errorf("failed to refresh token: %w", ErrTokenExpired)
	}

	return p.token.AccessToken, nil
}

func (p *GCPTokenProvider) expired() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.token.Expiry.Add(-p.expiryDelta).Before(time.Now())
}

func getToken(ctx context.Context) (*oauth2.Token, error) {
	creds, err := google.FindDefaultCredentials(ctx)
	if err != nil {
		return nil, err
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		return nil, err
	}

	return token, nil
}
