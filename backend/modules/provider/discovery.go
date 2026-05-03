package provider

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// AccountProvider exposes available accounts to ModelDiscoverer implementations.
type AccountProvider interface {
	GetAvailableAccounts(providerType string) ([]*Account, error)
}

// ModelDiscoverer is implemented by providers that can fetch their model list dynamically.
type ModelDiscoverer interface {
	DiscoverModels(ctx context.Context, accounts AccountProvider) ([]Model, error)
}

// EffortVariant describes one reasoning-effort variant of a base model.
type EffortVariant struct {
	Suffix string
	Label  string
}

// GeminiThinkingEfforts mirrors the budget choices Gemini Pro models accept.
var GeminiThinkingEfforts = []EffortVariant{
	{Suffix: "low", Label: "Low"},
	{Suffix: "high", Label: "High"},
}

// GeminiEffortsFor returns thinking-budget variants applicable to a Gemini model id.
func GeminiEffortsFor(modelID string) []EffortVariant {
	lower := strings.ToLower(modelID)
	if strings.Contains(lower, "pro") {
		return GeminiThinkingEfforts
	}
	return nil
}

// ExpandWithVariants returns the base model followed by one entry per effort variant.
func ExpandWithVariants(prefix, baseID, baseName, ownedBy string, variants []EffortVariant) []Model {
	result := []Model{
		{ID: prefix + baseID, Name: baseName, OwnedBy: ownedBy},
	}
	for _, v := range variants {
		result = append(result, Model{
			ID:      prefix + baseID + ":" + v.Suffix,
			Name:    baseName + " (" + v.Label + ")",
			OwnedBy: ownedBy,
		})
	}
	return result
}

type modelCacheEntry struct {
	models    []Model
	expiresAt time.Time
}

type modelCache struct {
	mu      sync.RWMutex
	entries map[string]modelCacheEntry
	ttl     time.Duration
}

func newModelCache(ttl time.Duration) *modelCache {
	return &modelCache{
		entries: make(map[string]modelCacheEntry),
		ttl:     ttl,
	}
}

func (c *modelCache) get(providerType string) ([]Model, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[providerType]
	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}
	return entry.models, true
}

func (c *modelCache) set(providerType string, models []Model) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[providerType] = modelCacheEntry{
		models:    models,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *modelCache) invalidate(providerType string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, providerType)
}

func (c *modelCache) invalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]modelCacheEntry)
}

func dedupModels(models []Model) []Model {
	seen := make(map[string]bool, len(models))
	out := make([]Model, 0, len(models))
	for _, m := range models {
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		out = append(out, m)
	}
	return out
}

func sortModels(models []Model) []Model {
	sort.SliceStable(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})
	return models
}

func firstOAuthOrAPIKeyAccount(accounts AccountProvider, providerType string) *Account {
	if accounts == nil {
		return nil
	}
	list, err := accounts.GetAvailableAccounts(providerType)
	if err != nil || len(list) == 0 {
		return nil
	}
	for _, a := range list {
		if a == nil {
			continue
		}
		if a.AuthToken() != "" {
			return a
		}
	}
	return nil
}
