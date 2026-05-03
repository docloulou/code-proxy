package provider

import (
	"context"
	"fmt"
	"strings"
)

func (p *GenericOpenAI) DiscoverModels(ctx context.Context, accounts AccountProvider) ([]Model, error) {
	if accounts == nil {
		return nil, fmt.Errorf("no account provider")
	}

	list, err := accounts.GetAvailableAccounts("generic-openai")
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("no generic-openai accounts available")
	}

	out := make([]Model, 0)
	for _, a := range list {
		if a == nil || a.AuthToken() == "" {
			continue
		}

		baseURL := a.BaseURL()
		if baseURL == "" {
			subtype := ""
			if a.Metadata != nil {
				subtype = a.Metadata["provider_subtype"]
			}
			baseURL = inferBaseURL(subtype, "")
		}
		if baseURL == "" {
			continue
		}

		prefix := genericPrefixForBaseURL(baseURL, a.Metadata)
		if prefix == "" {
			continue
		}

		models, err := fetchOpenAIModels(ctx, strings.TrimRight(baseURL, "/"), a.AuthToken())
		if err != nil {
			continue
		}

		for _, m := range models {
			baseID := strings.TrimSpace(m.ID)
			if baseID == "" {
				continue
			}
			owner := m.OwnedBy
			if owner == "" {
				owner = strings.TrimSuffix(prefix, "/")
			}
			out = append(out, Model{
				ID:      prefix + baseID,
				Name:    baseID,
				OwnedBy: owner,
			})
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no generic-openai models discovered")
	}
	return sortModels(dedupModels(out)), nil
}

func genericPrefixForBaseURL(baseURL string, metadata map[string]string) string {
	if metadata != nil {
		if sub := metadata["provider_subtype"]; sub != "" {
			return sub + "/"
		}
	}
	lower := strings.ToLower(baseURL)
	switch {
	case strings.Contains(lower, "deepseek"):
		return "deepseek/"
	case strings.Contains(lower, "groq"):
		return "groq/"
	case strings.Contains(lower, "together"):
		return "together/"
	case strings.Contains(lower, "fireworks"):
		return "fireworks/"
	case strings.Contains(lower, "mistral"):
		return "mistral/"
	case strings.Contains(lower, "perplexity"):
		return "perplexity/"
	case strings.Contains(lower, "ollama") || strings.Contains(lower, "11434"):
		return "ollama/"
	}
	return ""
}
