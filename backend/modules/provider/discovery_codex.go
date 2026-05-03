package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type codexAppServerModel struct {
	ID                      string `json:"id"`
	Model                   string `json:"model"`
	DisplayName             string `json:"displayName"`
	Description             string `json:"description"`
	Hidden                  bool   `json:"hidden"`
	SupportedReasoningEffortsRaw []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
	DefaultReasoningEffort string   `json:"defaultReasoningEffort"`
	InputModalities        []string `json:"inputModalities"`
	IsDefault              bool     `json:"isDefault"`
}

func (c *Codex) DiscoverModels(ctx context.Context, _ AccountProvider) ([]Model, error) {
	if !CLIBinaryAvailable("codex") {
		return nil, fmt.Errorf("codex binary not found")
	}

	models, err := fetchCodexAppServerModels(ctx, c.workDir)
	if err != nil {
		return nil, err
	}

	out := make([]Model, 0, len(models)*2*5)
	for _, m := range models {
		if m.Hidden {
			continue
		}
		baseID := m.ID
		if baseID == "" {
			baseID = m.Model
		}
		if baseID == "" {
			continue
		}
		baseName := m.DisplayName
		if baseName == "" {
			baseName = baseID
		}

		efforts := codexEffortsFromModel(m)

		out = append(out, ExpandWithVariants("cli-codex/", baseID, baseName, "openai", efforts)...)
		out = append(out, ExpandWithVariants("codex/", baseID, baseName, "openai", efforts)...)
	}

	return sortModels(dedupModels(out)), nil
}

func codexEffortsFromModel(m codexAppServerModel) []EffortVariant {
	if len(m.SupportedReasoningEffortsRaw) == 0 {
		return nil
	}
	out := make([]EffortVariant, 0, len(m.SupportedReasoningEffortsRaw))
	for _, e := range m.SupportedReasoningEffortsRaw {
		level := strings.TrimSpace(e.ReasoningEffort)
		if level == "" || level == m.DefaultReasoningEffort {
			continue
		}
		out = append(out, EffortVariant{
			Suffix: level,
			Label:  strings.ToUpper(level[:1]) + level[1:],
		})
	}
	return out
}

func fetchCodexAppServerModels(ctx context.Context, workDir string) ([]codexAppServerModel, error) {
	cmd := exec.CommandContext(ctx, "codex", "app-server")
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
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"code-proxy","version":"1"},"protocolVersion":1}}`,
		`{"jsonrpc":"2.0","id":2,"method":"model/list","params":{}}`,
	}
	for _, req := range requests {
		if _, err := fmt.Fprintln(stdin, req); err != nil {
			return nil, fmt.Errorf("write request: %w", err)
		}
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	type listResponse struct {
		ID     int `json:"id"`
		Result struct {
			Data []codexAppServerModel `json:"data"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var resp listResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.ID != 2 {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("codex model/list: %s", resp.Error.Message)
		}
		return resp.Result.Data, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan stdout: %w", err)
	}
	return nil, fmt.Errorf("codex app-server did not return model/list response")
}

