package provider

import "context"

func (c *Claude) DiscoverModels(ctx context.Context, _ AccountProvider) ([]Model, error) {
	return DiscoverClaudeAgentACPModels(ctx, c.workDir)
}

func (c *ClaudeACP) DiscoverModels(ctx context.Context, _ AccountProvider) ([]Model, error) {
	return DiscoverClaudeAgentACPModels(ctx, c.manager.workDir)
}
