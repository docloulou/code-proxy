package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type openAIAPIModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
}

type openAIModelsResponse struct {
	Data []openAIAPIModel `json:"data"`
}

func (p *OpenAIAPI) DiscoverModels(ctx context.Context, accounts AccountProvider) ([]Model, error) {
	acct := firstOAuthOrAPIKeyAccount(accounts, "openai-api")
	if acct == nil {
		return nil, fmt.Errorf("no openai-api account available")
	}

	models, err := fetchOpenAIModels(ctx, "https://api.openai.com", acct.AuthToken())
	if err != nil {
		return nil, err
	}

	out := make([]Model, 0, len(models)*2)
	for _, m := range models {
		baseID := strings.TrimSpace(m.ID)
		if baseID == "" {
			continue
		}
		owner := m.OwnedBy
		if owner == "" {
			owner = "openai"
		}

		out = append(out, Model{
			ID:      "openai/" + baseID,
			Name:    baseID,
			OwnedBy: owner,
		})

		if isCodexModel(baseID) {
			out = append(out, Model{
				ID:      "codex/" + baseID,
				Name:    baseID,
				OwnedBy: owner,
			})
			out = append(out, Model{
				ID:      "cx/" + baseID,
				Name:    baseID,
				OwnedBy: owner,
			})
		}
	}
	return sortModels(dedupModels(out)), nil
}

func isCodexModel(id string) bool {
	lower := strings.ToLower(id)
	return strings.Contains(lower, "codex") ||
		strings.HasPrefix(lower, "gpt-5") ||
		strings.HasPrefix(lower, "o3") ||
		strings.HasPrefix(lower, "o4")
}

func fetchOpenAIModels(ctx context.Context, baseURL, token string) ([]openAIAPIModel, error) {
	if token == "" {
		return nil, fmt.Errorf("openai token is empty")
	}
	httpReq, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai /v1/models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai /v1/models returned %d: %s", resp.StatusCode, string(body))
	}

	var parsed openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode openai models: %w", err)
	}
	return parsed.Data, nil
}
