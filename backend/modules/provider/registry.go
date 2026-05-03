package provider

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// DefaultModelCacheTTL is how long discovered models are cached before re-fetching.
const DefaultModelCacheTTL = 1 * time.Hour

// Registry manages registered providers and resolves model → provider
type Registry struct {
	mu          sync.RWMutex
	providers   map[string]Provider
	defaults    string
	cache       *modelCache
	accountProv AccountProvider
}

// NewRegistry creates an empty registry with a default model-cache TTL.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
		cache:     newModelCache(DefaultModelCacheTTL),
	}
}

// SetAccountProvider wires the account source used for dynamic model discovery.
func (r *Registry) SetAccountProvider(ap AccountProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accountProv = ap
}

// SetCacheTTL overrides the default discovery cache TTL.
func (r *Registry) SetCacheTTL(ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache = newModelCache(ttl)
}

// Register adds a provider to the registry
func (r *Registry) Register(providerType string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[providerType] = p
	// First registered becomes default
	if r.defaults == "" {
		r.defaults = providerType
	}
}

// SetDefault sets the default provider
func (r *Registry) SetDefault(providerType string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaults = providerType
}

// Get returns a provider by type
func (r *Registry) Get(providerType string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[providerType]
	return p, ok
}

// ResolveProvider resolves a model ID to the correct provider and a cleaned model ID
func (r *Registry) ResolveProvider(model string) (Provider, string, string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providerType, cleanModel := parseModelPrefix(model)

	if providerType != "" {
		if p, ok := r.providers[providerType]; ok {
			return p, providerType, cleanModel, nil
		}
		return nil, "", "", fmt.Errorf("provider %q not found for model %q", providerType, model)
	}

	// No prefix: uses default provider
	if r.defaults != "" {
		if p, ok := r.providers[r.defaults]; ok {
			return p, r.defaults, cleanModel, nil
		}
	}

	// Fallback: first registered provider
	for pt, p := range r.providers {
		return p, pt, cleanModel, nil
	}

	return nil, "", "", fmt.Errorf("no providers registered")
}

// AllModels returns the merged model list across all providers, using the
// dynamic discovery cache when available and falling back to static Models().
func (r *Registry) AllModels() []Model {
	r.mu.RLock()
	providers := make(map[string]Provider, len(r.providers))
	for k, v := range r.providers {
		providers[k] = v
	}
	cache := r.cache
	accounts := r.accountProv
	r.mu.RUnlock()

	var models []Model
	for pt, p := range providers {
		models = append(models, modelsForProvider(pt, p, cache, accounts)...)
	}
	return models
}

// ModelsForProvider returns models for a specific provider (cached when possible).
func (r *Registry) ModelsForProvider(providerType string) []Model {
	r.mu.RLock()
	p, ok := r.providers[providerType]
	cache := r.cache
	accounts := r.accountProv
	r.mu.RUnlock()

	if !ok {
		return nil
	}
	return modelsForProvider(providerType, p, cache, accounts)
}

// RefreshModels invalidates the cache and re-runs DiscoverModels on every provider
// that implements ModelDiscoverer. Returns a map of provider types to discovery errors
// (empty for successes).
func (r *Registry) RefreshModels(ctx context.Context) map[string]error {
	r.mu.RLock()
	providers := make(map[string]Provider, len(r.providers))
	for k, v := range r.providers {
		providers[k] = v
	}
	cache := r.cache
	accounts := r.accountProv
	r.mu.RUnlock()

	if cache != nil {
		cache.invalidateAll()
	}

	errs := make(map[string]error)
	for pt, p := range providers {
		disc, ok := p.(ModelDiscoverer)
		if !ok {
			continue
		}
		discCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		models, err := disc.DiscoverModels(discCtx, accounts)
		cancel()
		if err != nil {
			errs[pt] = err
			log.Printf("[REGISTRY] Discover %s failed: %v", pt, err)
			continue
		}
		if cache != nil {
			cache.set(pt, models)
		}
		log.Printf("[REGISTRY] Discover %s: %d models cached", pt, len(models))
	}
	return errs
}

func modelsForProvider(providerType string, p Provider, cache *modelCache, accounts AccountProvider) []Model {
	if cache != nil {
		if cached, ok := cache.get(providerType); ok {
			return cached
		}
	}

	if disc, ok := p.(ModelDiscoverer); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		models, err := disc.DiscoverModels(ctx, accounts)
		if err != nil {
			log.Printf("[REGISTRY] Discover %s on-demand failed: %v", providerType, err)
			return nil
		}
		if cache != nil {
			cache.set(providerType, models)
		}
		return models
	}

	return nil
}

// ProviderStatus returns status info for all registered providers
type ProviderStatusInfo struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Available bool   `json:"available"`
}

func (r *Registry) ProviderStatuses() []ProviderStatusInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var statuses []ProviderStatusInfo
	for pt, p := range r.providers {
		statuses = append(statuses, ProviderStatusInfo{
			Type:      pt,
			Name:      p.Name(),
			Category:  p.Category(),
			Available: p.IsAvailable(),
		})
	}
	return statuses
}

// ListProviders returns the registered provider types
func (r *Registry) ListProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.providers))
	for pt := range r.providers {
		types = append(types, pt)
	}
	return types
}

// parseModelPrefix extracts the provider type from the model prefix
// Ex: "cc/claude-opus-4-6" -> ("claude-cli", "claude-opus-4-6")
// Ex: "openai/gpt-4o" -> ("openai-api", "gpt-4o")
// Ex: "sonnet" -> ("", "sonnet")
func parseModelPrefix(model string) (providerType, cleanModel string) {
	lower := strings.ToLower(model)

	// Explicit prefixes
	prefixes := map[string]string{
		// CLI prefixes (local binary execution)
		"cli-cc/":    "claude-cli",
		"cli-codex/": "codex-cli",
		"cli-gc/":    "gemini-cli",
		// OAuth/API prefixes
		"cc/":         "claude-cli",
		"cx/":         "codex-cli",
		"codex/":      "codex-cli",
		"gc/":         "gemini-cli",
		"gemini-cli/": "gemini-cli",
		"anthropic/":  "anthropic-api",
		"openai/":     "openai-api",
		"gemini/":     "gemini-api",
		"deepseek/":   "generic-openai",
		"groq/":       "generic-openai",
		"together/":   "generic-openai",
		"ollama/":     "generic-openai",
	}

	for prefix, pt := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return pt, model[len(prefix):]
		}
	}

	// Detection by model name (no prefix)
	if strings.HasPrefix(lower, "gpt-") || strings.HasPrefix(lower, "o1") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4") {
		return "openai-api", model
	}

	return "", model
}
