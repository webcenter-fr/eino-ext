package copilot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func (m *CopilotModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	body, err := m.buildChatRequest(in, true, opts...)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
	setAuthHeaders(req, m.lockedToken.get())
	req.Header.Set("Accept", "text/event-stream")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: request failed")
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, errors.Errorf("copilot: API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	sr, sw := schema.Pipe[*schema.Message](1)

	go func() {
		defer resp.Body.Close()
		defer sw.Close()

		if err := streamEvents(ctx, resp.Body, sw); err != nil {
			sw.Send(nil, err)
		}
	}()

	return sr, nil
}

func streamEvents(ctx context.Context, body io.Reader, sw *schema.StreamWriter[*schema.Message]) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var toolAccum map[int]*toolCallAccumState
	var reasoningOpen bool

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}

		if strings.TrimSpace(data) == "[DONE]" {
			return nil
		}

		var chunk copilotChatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return errors.Wrap(err, "copilot: failed to parse stream chunk")
		}

		for _, choice := range chunk.Choices {
			delta := choice.Delta

			// Reasoning text: emit as reasoning content, skip everything else for this chunk.
			if delta.ReasoningText != "" || delta.ReasoningOpaque != "" {
				reasoningOpen = true
				sw.Send(&schema.Message{
					Role:             schema.Assistant,
					ReasoningContent: delta.ReasoningText,
				}, nil)
				continue
			}

			// Transition to content/tool-calls after a reasoning block.
			if reasoningOpen && delta.Content == "" && len(delta.ToolCalls) == 0 {
				reasoningOpen = false
				continue
			}

			msg := &schema.Message{Role: schema.Assistant}

			if delta.Content != "" {
				msg.Content = delta.Content
			}

			for _, tc := range delta.ToolCalls {
				if tc.Index == nil {
					continue
				}
				idx := *tc.Index
				if toolAccum == nil {
					toolAccum = make(map[int]*toolCallAccumState)
				}
				st, ok := toolAccum[idx]
				if !ok {
					st = &toolCallAccumState{}
					toolAccum[idx] = st
				}
				if tc.ID != "" {
					st.id = tc.ID
				}
				if tc.Function.Name != "" {
					st.name = tc.Function.Name
				}
				st.args += tc.Function.Arguments
			}

			// Emit accumulated tool calls on finish.
			if choice.FinishReason != nil && len(toolAccum) > 0 {
				for _, st := range toolAccum {
					msg.ToolCalls = append(msg.ToolCalls, schema.ToolCall{
						ID: st.id,
						Function: schema.FunctionCall{
							Name:      st.name,
							Arguments: st.args,
						},
					})
				}
				toolAccum = nil
			}

			if msg.Content != "" || len(msg.ToolCalls) > 0 {
				sw.Send(msg, nil)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return errors.Wrap(err, "copilot: stream scanner error")
	}
	return nil
}

type toolCallAccumState struct {
	id, name, args string
}
