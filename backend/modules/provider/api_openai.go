package provider

import (
	"context"
	"fmt"
)

// OpenAIAPI is the provider for the OpenAI API (direct pass-through)
type OpenAIAPI struct{}

// NewOpenAIAPI creates an OpenAI API provider
func NewOpenAIAPI() *OpenAIAPI {
	return &OpenAIAPI{}
}

func (p *OpenAIAPI) Name() string      { return "openai-api" }
func (p *OpenAIAPI) Category() string  { return "api" }
func (p *OpenAIAPI) IsAvailable() bool { return true }

func (p *OpenAIAPI) Models() []Model {
	return nil
}

func (p *OpenAIAPI) Execute(ctx context.Context, req *Request) (<-chan Event, error) {
	if req.Account == nil {
		return nil, fmt.Errorf("OpenAI API requires a configured account (API key or OAuth)")
	}

	baseURL := "https://api.openai.com"
	authHeader := "Bearer " + req.Account.AuthToken()

	// Direct pass-through — format is already OpenAI
	return proxyExecute(ctx, req, baseURL, authHeader, nil, nil, nil)
}
