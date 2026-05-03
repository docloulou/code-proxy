package provider

import (
	"context"
	"fmt"
	"log"

	acp "github.com/coder/acp-go-sdk"
)

// ClaudeACP is the provider that uses the ACP protocol to communicate with the Claude Agent
type ClaudeACP struct {
	manager *acpManager
}

// NewClaudeACP creates an ACP provider with the configuration parameters
func NewClaudeACP(workDir, acpCommand, acpArgs string) *ClaudeACP {
	return &ClaudeACP{
		manager: newACPManager(workDir, acpCommand, acpArgs),
	}
}

func (c *ClaudeACP) Category() string { return "cli" }

func (c *ClaudeACP) IsAvailable() bool {
	// ACP uses a local subprocess (claude-code-acp by default).
	return CLIBinaryAvailable(c.manager.acpCommand)
}

func (c *ClaudeACP) Name() string { return "claude" }

func (c *ClaudeACP) Models() []Model {
	return nil
}

// Execute implements Provider.Execute — sends prompt via ACP
func (c *ClaudeACP) Execute(ctx context.Context, req *Request) (<-chan Event, error) {
	// Extract prompt from OpenAI RawBody
	systemPrompt, prompt := ExtractPrompt(req.RawBody)
	model := req.Model
	effort := req.Effort

	// Create session
	sess, err := c.manager.getSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("ACP session: %w", err)
	}

	events := sess.eventCh

	// Build prompt content
	var promptText string
	if systemPrompt != "" {
		promptText = fmt.Sprintf("[System Instructions]\n%s\n[End System Instructions]\n\n%s", systemPrompt, prompt)
	} else {
		promptText = prompt
	}

	log.Printf("[ACP] Prompt: model=%s, effort=%s, %d chars", model, effort, len(promptText))

	// Send prompt in goroutine (blocks until agent finishes)
	go func() {
		defer close(events)
		defer c.manager.releaseSession(sess.sessionID)

		// Send prompt
		conn := c.manager.conn
		if conn == nil {
			events <- Event{Type: "error", Text: "ACP connection lost"}
			return
		}

		resp, err := conn.Prompt(ctx, acp.PromptRequest{
			SessionId: sess.sessionID,
			Prompt:    []acp.ContentBlock{acp.TextBlock(promptText)},
		})
		if err != nil {
			log.Printf("[ACP] Prompt error: %v", err)
			events <- Event{Type: "error", Text: err.Error()}
			return
		}

		log.Printf("[ACP] Prompt done: stopReason=%s", resp.StopReason)
		events <- Event{Type: "done"}
	}()

	return events, nil
}

// Shutdown terminates the ACP subprocess
func (c *ClaudeACP) Shutdown() {
	c.manager.shutdown()
}
