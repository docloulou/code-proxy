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

type geminiAPIModel struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type geminiAPIModelsResponse struct {
	Models []geminiAPIModel `json:"models"`
}

func (p *GeminiAPI) DiscoverModels(ctx context.Context, accounts AccountProvider) ([]Model, error) {
	acct := firstOAuthOrAPIKeyAccount(accounts, "gemini-api")
	if acct == nil {
		return nil, fmt.Errorf("no gemini-api account available")
	}

	models, err := fetchGeminiAPIModels(ctx, acct)
	if err != nil {
		return nil, err
	}

	out := make([]Model, 0, len(models))
	for _, m := range models {
		baseID := strings.TrimPrefix(m.Name, "models/")
		baseID = strings.TrimSpace(baseID)
		if baseID == "" {
			continue
		}
		baseName := strings.TrimSpace(m.DisplayName)
		if baseName == "" {
			baseName = baseID
		}
		out = append(out, Model{
			ID:      "gemini/" + baseID,
			Name:    baseName,
			OwnedBy: "google",
		})
	}
	return sortModels(dedupModels(out)), nil
}

func fetchGeminiAPIModels(ctx context.Context, acct *Account) ([]geminiAPIModel, error) {
	endpoint := "https://generativelanguage.googleapis.com/v1beta/models?pageSize=200"
	httpReq, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	if acct.AuthMode == "oauth" && acct.AccessToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+acct.AccessToken)
	} else if acct.APIKey != "" {
		q := httpReq.URL.Query()
		q.Set("key", acct.APIKey)
		httpReq.URL.RawQuery = q.Encode()
	} else {
		return nil, fmt.Errorf("gemini account has no usable token")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini /v1beta/models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini /v1beta/models returned %d: %s", resp.StatusCode, string(body))
	}

	var parsed geminiAPIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode gemini models: %w", err)
	}
	return parsed.Models, nil
}

