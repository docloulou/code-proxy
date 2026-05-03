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

type geminiACPModel struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (c *GeminiCLI) DiscoverModels(ctx context.Context, _ AccountProvider) ([]Model, error) {
	if !CLIBinaryAvailable("gemini") {
		return nil, fmt.Errorf("gemini binary not found")
	}

	models, err := fetchGeminiACPModels(ctx, c.workDir)
	if err != nil {
		return nil, err
	}

	out := make([]Model, 0, len(models)*2*3)
	for _, m := range models {
		baseID := strings.TrimSpace(m.ModelID)
		if baseID == "" {
			continue
		}
		baseName := strings.TrimSpace(m.Name)
		if baseName == "" {
			baseName = baseID
		}

		efforts := GeminiEffortsFor(baseID)
		out = append(out, ExpandWithVariants("cli-gc/", baseID, baseName, "google", efforts)...)
		out = append(out, ExpandWithVariants("gc/", baseID, baseName, "google", efforts)...)
	}

	return sortModels(dedupModels(out)), nil
}

func fetchGeminiACPModels(ctx context.Context, workDir string) ([]geminiACPModel, error) {
	cmd := exec.CommandContext(ctx, "gemini", "--acp")
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
		return nil, fmt.Errorf("start gemini --acp: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":true,"writeTextFile":true},"terminal":true}}}`,
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":%q,"mcpServers":[]}}`, workDir),
	}
	for _, req := range requests {
		if _, err := fmt.Fprintln(stdin, req); err != nil {
			return nil, fmt.Errorf("write request: %w", err)
		}
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	type sessionNewResp struct {
		ID     int `json:"id"`
		Result struct {
			Models struct {
				AvailableModels []geminiACPModel `json:"availableModels"`
			} `json:"models"`
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
		var resp sessionNewResp
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.ID != 2 {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("gemini session/new: %s", resp.Error.Message)
		}
		return resp.Result.Models.AvailableModels, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan stdout: %w", err)
	}
	return nil, fmt.Errorf("gemini --acp did not return models in session/new response")
}
