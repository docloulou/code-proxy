package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

const claudeAgentACPBinary = "claude-agent-acp"

type claudeACPModelInfo struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type claudeACPConfigOption struct {
	ID            string `json:"id"`
	Category      string `json:"category"`
	Type          string `json:"type"`
	CurrentValue  string `json:"currentValue"`
	Options       []struct {
		Value string `json:"value"`
		Name  string `json:"name"`
	} `json:"options"`
}

type claudeACPSessionResult struct {
	SessionID string `json:"sessionId"`
	Models    struct {
		AvailableModels []claudeACPModelInfo `json:"availableModels"`
		CurrentModelID  string               `json:"currentModelId"`
	} `json:"models"`
	ConfigOptions []claudeACPConfigOption `json:"configOptions"`
}

// DiscoverClaudeAgentACPModels spawns the claude-agent-acp binary, performs the
// ACP handshake, and returns the agent's advertised models with their effort variants.
func DiscoverClaudeAgentACPModels(ctx context.Context, workDir string) ([]Model, error) {
	if !CLIBinaryAvailable(claudeAgentACPBinary) {
		return nil, fmt.Errorf("%s binary not found in PATH", claudeAgentACPBinary)
	}

	result, err := probeClaudeAgentACP(ctx, workDir)
	if err != nil {
		return nil, err
	}

	efforts := claudeACPEffortVariants(result.ConfigOptions)

	out := make([]Model, 0, len(result.Models.AvailableModels)*2*(len(efforts)+1))
	for _, m := range result.Models.AvailableModels {
		baseID := strings.TrimSpace(m.ModelID)
		if baseID == "" {
			continue
		}

		canonicalID := canonicalClaudeModelID(baseID, m.Description)
		baseName := strings.TrimSpace(m.Name)
		if baseName == "" {
			baseName = canonicalID
		}

		modelEfforts := efforts
		if !claudeModelSupportsEffort(canonicalID) {
			modelEfforts = nil
		}

		out = append(out, ExpandWithVariants("cli-cc/", canonicalID, baseName, "anthropic", modelEfforts)...)
		out = append(out, ExpandWithVariants("cc/", canonicalID, baseName, "anthropic", modelEfforts)...)
	}

	return sortModels(dedupModels(out)), nil
}

func claudeACPEffortVariants(options []claudeACPConfigOption) []EffortVariant {
	for _, opt := range options {
		if opt.ID != "effort" && opt.Category != "thought_level" {
			continue
		}
		variants := make([]EffortVariant, 0, len(opt.Options))
		for _, o := range opt.Options {
			suffix := strings.TrimSpace(o.Value)
			if suffix == "" {
				continue
			}
			label := strings.TrimSpace(o.Name)
			if label == "" {
				label = strings.ToUpper(suffix[:1]) + suffix[1:]
			}
			variants = append(variants, EffortVariant{Suffix: suffix, Label: label})
		}
		return variants
	}
	return nil
}

func claudeModelSupportsEffort(canonicalID string) bool {
	lower := strings.ToLower(canonicalID)
	return !strings.Contains(lower, "haiku")
}

var claudeFamilyVersionPattern = regexp.MustCompile(`(?i)\b(opus|sonnet|haiku)\s*([0-9]+(?:\.[0-9]+)*)\b`)

func canonicalClaudeModelID(modelID, description string) string {
	if matches := claudeFamilyVersionPattern.FindStringSubmatch(description); len(matches) == 3 {
		family := strings.ToLower(matches[1])
		version := strings.ReplaceAll(matches[2], ".", "-")
		return fmt.Sprintf("claude-%s-%s", family, version)
	}
	return modelID
}

func probeClaudeAgentACP(ctx context.Context, workDir string) (*claudeACPSessionResult, error) {
	cmd := exec.CommandContext(ctx, claudeAgentACPBinary)
	if workDir != "" {
		cmd.Dir = workDir
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", claudeAgentACPBinary, err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	cwd := workDir
	if cwd == "" {
		cwd = "/tmp"
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":true,"writeTextFile":true},"terminal":true}}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":%q,"mcpServers":[]}}`, cwd),
	}
	for _, req := range requests {
		if _, err := fmt.Fprintln(stdin, req); err != nil {
			return nil, fmt.Errorf("write request: %w", err)
		}
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	type rpcResponse struct {
		ID     int                    `json:"id"`
		Result claudeACPSessionResult `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.ID != 2 {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("%s session/new: %s", claudeAgentACPBinary, resp.Error.Message)
		}
		return &resp.Result, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan stdout: %w", err)
	}
	return nil, fmt.Errorf("%s did not return session/new response", claudeAgentACPBinary)
}
