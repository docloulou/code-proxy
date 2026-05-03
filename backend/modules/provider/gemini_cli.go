package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// GeminiCLI is the provider for Google Gemini CLI
type GeminiCLI struct {
	workDir string
}

// NewGeminiCLI creates a Gemini CLI provider
func NewGeminiCLI(workDir string) *GeminiCLI {
	return &GeminiCLI{workDir: workDir}
}

func (c *GeminiCLI) Name() string      { return "gemini-cli" }
func (c *GeminiCLI) Category() string  { return "cli" }
func (c *GeminiCLI) IsAvailable() bool { return CLIBinaryAvailable("gemini") }

func (c *GeminiCLI) Models() []Model {
	return nil
}

func (c *GeminiCLI) Execute(ctx context.Context, req *Request) (<-chan Event, error) {
	systemPrompt, userPrompt := ExtractPrompt(req.RawBody)
	prompt := strings.TrimSpace(userPrompt)
	if prompt == "" && strings.TrimSpace(systemPrompt) != "" {
		prompt = strings.TrimSpace(systemPrompt)
	} else if prompt != "" && strings.TrimSpace(systemPrompt) != "" {
		prompt = strings.TrimSpace(systemPrompt) + "\n\n" + prompt
	}

	model := req.Model
	if model == "" {
		model = "gemini-2.5-flash"
	}

	if strings.TrimSpace(prompt) == "" {
		events := make(chan Event, 2)
		go func() {
			defer close(events)
			events <- Event{Type: "text", Text: "(Gemini prompt is empty - no instructions provided.)"}
			events <- Event{Type: "done"}
		}()
		return events, nil
	}

	args := []string{
		"--model", model,
		"--sandbox",
		"--yolo",
		"--output-format", "stream-json",
	}

	log.Printf("[GEMINI-CLI] Spawning: gemini %s", strings.Join(args, " "))
	log.Printf("[GEMINI-CLI] Prompt: %d chars", len(prompt))

	cmd := exec.CommandContext(ctx, "gemini", args...)
	if c.workDir != "" {
		cmd.Dir = c.workDir
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start gemini: %w", err)
	}

	events := make(chan Event, 128)
	stdinResultCh := make(chan geminiStdinResult, 1)

	go func() {
		_, writeErr := stdin.Write([]byte(prompt))
		closeErr := stdin.Close()
		stdinResultCh <- geminiStdinResult{writeErr: writeErr, closeErr: closeErr}
	}()

	go func() {
		defer close(events)

		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

		var sentAnyText bool
		var lastStdoutLine string
		var resultStatus string

		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			lastStdoutLine = line

			var msg geminiStreamMessage
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				log.Printf("[GEMINI-CLI] Non-JSON stdout: %s", truncate(line, 300))
				continue
			}

			switch msg.Type {
			case "message":
				if msg.Role == "assistant" && msg.Content != "" {
					events <- Event{Type: "text", Text: msg.Content}
					sentAnyText = true
				}
			case "result":
				resultStatus = msg.Status
				if msg.Status != "" && msg.Status != "success" {
					log.Printf("[GEMINI-CLI] Result status: %s", msg.Status)
				}
			case "error":
				if msg.Content != "" {
					resultStatus = msg.Content
				}
			}
		}

		scanErr := scanner.Err()
		stdinResult := <-stdinResultCh
		waitErr := cmd.Wait()
		stderrStr := strings.TrimSpace(stderrBuf.String())

		if stderrStr != "" {
			log.Printf("[GEMINI-CLI] stderr: %s", truncate(stderrStr, 1000))
		}

		if !sentAnyText {
			events <- Event{Type: "text", Text: buildGeminiEmptyResponseMessage(stdinResult.writeErr, stdinResult.closeErr, scanErr, waitErr, stderrStr, lastStdoutLine, resultStatus)}
		} else if diagnostic := buildGeminiPostTextDiagnostic(stdinResult.writeErr, stdinResult.closeErr, scanErr, waitErr, stderrStr, resultStatus); diagnostic != "" {
			events <- Event{Type: "text", Text: diagnostic}
		}

		events <- Event{Type: "done"}
	}()

	return events, nil
}

type geminiStdinResult struct {
	writeErr error
	closeErr error
}

type geminiStreamMessage struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	Status  string `json:"status,omitempty"`
}

func buildGeminiEmptyResponseMessage(writeErr, closeErr, scanErr, waitErr error, stderrStr, lastStdoutLine, resultStatus string) string {
	msg := "(Gemini CLI returned empty response)"
	if writeErr != nil {
		msg += fmt.Sprintf(" write error: %v", writeErr)
	}
	if closeErr != nil {
		msg += fmt.Sprintf(" close error: %v", closeErr)
	}
	if scanErr != nil {
		msg += fmt.Sprintf(" scan error: %v", scanErr)
	}
	if waitErr != nil {
		msg += fmt.Sprintf(" exit error: %v", waitErr)
	}
	if resultStatus != "" && resultStatus != "success" {
		msg += " status: " + resultStatus
	}
	if stderrStr != "" {
		msg += " stderr: " + lastNonEmptyLine(stderrStr)
	} else if lastStdoutLine != "" {
		msg += " stdout: " + truncate(lastStdoutLine, 300)
	}
	return msg
}

func buildGeminiPostTextDiagnostic(writeErr, closeErr, scanErr, waitErr error, stderrStr, resultStatus string) string {
	var parts []string
	if writeErr != nil {
		parts = append(parts, fmt.Sprintf("write error: %v", writeErr))
	}
	if closeErr != nil {
		parts = append(parts, fmt.Sprintf("close error: %v", closeErr))
	}
	if scanErr != nil {
		parts = append(parts, fmt.Sprintf("scan error: %v", scanErr))
	}
	if waitErr != nil {
		parts = append(parts, fmt.Sprintf("exit error: %v", waitErr))
	}
	if resultStatus != "" && resultStatus != "success" {
		parts = append(parts, "status: "+truncate(resultStatus, 300))
	}
	if waitErr != nil && stderrStr != "" {
		parts = append(parts, "stderr: "+lastNonEmptyLine(stderrStr))
	}
	if len(parts) == 0 {
		return ""
	}
	return "\n(Gemini CLI warning: " + strings.Join(parts, "; ") + ")\n"
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return truncate(line, 300)
		}
	}
	return ""
}
