package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/store"
)

const collShareLinks = "share_links"

var (
	ErrShareLinkExpired               = errors.New("share link expired")
	ErrShareLinkDownloadLimitExceeded = errors.New("share link download limit exceeded")
)

type ShareLink struct {
	Token         string    `json:"token"`
	Username      string    `json:"username"`
	Path          string    `json:"path"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	DownloadCount int       `json:"download_count"`
	MaxDownloads  int       `json:"max_downloads"`
}

type shareLinkRepository struct {
	store *store.Store
	mu    sync.Mutex
}

func newShareLinkRepository(st *store.Store) *shareLinkRepository {
	if st == nil {
		return nil
	}
	return &shareLinkRepository{store: st}
}

func (r *shareLinkRepository) Create(username, filePath string, ttl time.Duration, maxDownloads int) (*ShareLink, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("share links are not configured")
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("username is required")
	}
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return nil, errors.New("path is required")
	}
	if ttl <= 0 {
		return nil, errors.New("ttl must be greater than zero")
	}
	if maxDownloads < 0 {
		return nil, errors.New("max_downloads cannot be negative")
	}
	token, err := generateShareToken()
	if err != nil {
		return nil, err
	}
	link := &ShareLink{
		Token:         token,
		Username:      username,
		Path:          filePath,
		CreatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().UTC().Add(ttl),
		DownloadCount: 0,
		MaxDownloads:  maxDownloads,
	}
	if err := r.store.Put(collShareLinks, link.Token, link); err != nil {
		return nil, err
	}
	return link, nil
}

func (r *shareLinkRepository) Get(token string) (*ShareLink, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("share links are not configured")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("token is required")
	}
	var link ShareLink
	if err := r.store.Get(collShareLinks, token, &link); err != nil {
		return nil, err
	}
	return &link, nil
}

func (r *shareLinkRepository) Increment(token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	link, err := r.getUnlocked(token)
	if err != nil {
		return err
	}
	link.DownloadCount++
	return r.store.Put(collShareLinks, token, link)
}

func (r *shareLinkRepository) ReserveDownload(token string, now time.Time) (*ShareLink, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("share links are not configured")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	link, err := r.getUnlocked(token)
	if err != nil {
		return nil, err
	}
	if !link.ExpiresAt.IsZero() && now.After(link.ExpiresAt) {
		return nil, ErrShareLinkExpired
	}
	if link.MaxDownloads > 0 && link.DownloadCount >= link.MaxDownloads {
		return nil, ErrShareLinkDownloadLimitExceeded
	}
	link.DownloadCount++
	if err := r.store.Put(collShareLinks, token, link); err != nil {
		return nil, err
	}
	return link, nil
}

func (r *shareLinkRepository) ReleaseDownload(token string) error {
	if r == nil || r.store == nil {
		return errors.New("share links are not configured")
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	link, err := r.getUnlocked(token)
	if err != nil {
		return err
	}
	if link.DownloadCount > 0 {
		link.DownloadCount--
	}
	return r.store.Put(collShareLinks, token, link)
}

func (r *shareLinkRepository) getUnlocked(token string) (*ShareLink, error) {
	link, err := r.Get(token)
	if err != nil {
		return nil, err
	}
	return link, nil
}

func (r *shareLinkRepository) ListByUsername(username string) ([]*ShareLink, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("share links are not configured")
	}
	username = strings.TrimSpace(username)
	var all []*ShareLink
	if err := r.store.List(collShareLinks, &all); err != nil {
		return nil, err
	}
	out := make([]*ShareLink, 0, len(all))
	for _, item := range all {
		if item == nil {
			continue
		}
		if username != "" && !strings.EqualFold(item.Username, username) {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (r *shareLinkRepository) Delete(token string) error {
	if r == nil || r.store == nil {
		return errors.New("share links are not configured")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("token is required")
	}
	return r.store.Delete(collShareLinks, token)
}

func generateShareToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "share_" + base64.RawURLEncoding.EncodeToString(raw), nil
}
