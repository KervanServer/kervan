package config

import (
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
)

type LiveConfig struct {
	path     string
	current  atomic.Pointer[Config]
	mu       sync.Mutex
	onReload []func(*Config)
}

func NewLiveConfig(path string) (*LiveConfig, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	lc := &LiveConfig{path: path}
	lc.current.Store(cfg)
	return lc, nil
}

func (lc *LiveConfig) Get() *Config {
	return lc.current.Load()
}

func (lc *LiveConfig) OnReload(fn func(*Config)) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.onReload = append(lc.onReload, fn)
}

func (lc *LiveConfig) Reload() error {
	cfg, err := Load(lc.path)
	if err != nil {
		return err
	}

	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.current.Store(cfg)
	for _, fn := range lc.onReload {
		fn(cfg)
	}
	return nil
}

func (lc *LiveConfig) WatchSignals() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	go func() {
		for range ch {
			_ = lc.Reload()
		}
	}()
}
