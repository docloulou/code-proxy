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

type anthropicAPIModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
}

type anthropicModelsResponse struct {
	Data []anthropicAPIModel `json:"data"`
}

func (p *AnthropicAPI) DiscoverModels(ctx context.Context, accounts AccountProvider) ([]Model, error) {
	acct := firstOAuthOrAPIKeyAccount(accounts, "anthropic-api")
	if acct == nil {
		return nil, fmt.Errorf("no anthropic-api account available")
	}

	models, err := fetchAnthropicModels(ctx, acct)
	if err != nil {
		return nil, err
	}

	out := make([]Model, 0, len(models))
	for _, m := range models {
		baseID := strings.TrimSpace(m.ID)
		if baseID == "" {
			continue
		}
		baseName := strings.TrimSpace(m.DisplayName)
		if baseName == "" {
			baseName = baseID
		}
		out = append(out, Model{
			ID:      "anthropic/" + baseID,
			Name:    baseName,
			OwnedBy: "anthropic",
		})
	}
	return sortModels(dedupModels(out)), nil
}

func fetchAnthropicModels(ctx context.Context, acct *Account) ([]anthropicAPIModel, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", anthropicBaseURL+"/v1/models?limit=1000", nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	if acct.AuthMode == "oauth" && acct.AccessToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+acct.AccessToken)
	} else if tok := acct.AuthToken(); tok != "" {
		httpReq.Header.Set("x-api-key", tok)
	} else {
		return nil, fmt.Errorf("anthropic account has no usable token")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic /v1/models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("anthropic /v1/models returned %d: %s", resp.StatusCode, string(body))
	}

	var parsed anthropicModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode anthropic models: %w", err)
	}
	return parsed.Data, nil
}
