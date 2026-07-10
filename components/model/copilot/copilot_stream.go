package copilot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func (m *CopilotModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	// GPT-5 routing: when the resolved model needs the Responses API, dispatch there.
	resolvedModel := m.resolveModel(opts...)
	if m.useResponsesAPI(resolvedModel) {
		return m.streamResponses(ctx, in, opts...)
	}

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
	setPerRequestHeaders(req, in)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: request failed")
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		bodyPreview := ""
		if readErr == nil {
			bodyPreview = redactErrorBody(bodyBytes)
		}
		return nil, errors.Errorf("copilot: API returned status %d: %s", resp.StatusCode, bodyPreview)
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
			// Flush any remaining accumulated tool calls that were never
			// closed by a finish-reason chunk (defensive).
			if len(toolAccum) > 0 {
				msg := &schema.Message{
					Role:      schema.Assistant,
					ToolCalls: flushToolAccumToolCalls(toolAccum),
				}
				sw.Send(msg, nil)
			}
			return nil
		}

		var chunk copilotChatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return errors.Wrap(err, "copilot: failed to parse stream chunk")
		}

		// Emit usage when the chunk carries it (stream_options.include_usage
		// usage-only final chunk typically has empty Choices).
		if chunk.Usage != nil {
			sw.Send(&schema.Message{
				Role: schema.Assistant,
				ResponseMeta: &schema.ResponseMeta{
					Usage: usageToTokenUsage(chunk.Usage),
				},
			}, nil)
		}

		for _, choice := range chunk.Choices {
			delta := choice.Delta

			msg := &schema.Message{Role: schema.Assistant}

			// Emit reasoning first (if present), then process content and
			// tool_calls in the same iteration. This handles the case where
			// reasoning_text/reasoning_opaque and content/tool_calls arrive in
			// the same chunk (the kilocode reference explicitly handles this).
			if delta.ReasoningText != "" || delta.ReasoningOpaque != "" {
				reasoningContent := delta.ReasoningText
				if reasoningContent == "" {
					reasoningContent = delta.ReasoningOpaque
				}
				// Persist opaque for multi-turn round-trip.
				if delta.ReasoningOpaque != "" {
					if msg.Extra == nil {
						msg.Extra = make(map[string]any)
					}
					msg.Extra["copilot_reasoning_opaque"] = delta.ReasoningOpaque
				}
				sw.Send(&schema.Message{
					Role:             schema.Assistant,
					ReasoningContent: reasoningContent,
				}, nil)
			}

			// Process content delta.
			if delta.Content != "" {
				msg.Content = delta.Content
			}

			// Process tool call deltas.
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

			// Set finish reason when present.
			if choice.FinishReason != nil {
				msg.ResponseMeta = &schema.ResponseMeta{
					FinishReason: mapFinishReason(*choice.FinishReason),
				}
			}

			// Emit accumulated tool calls on finish, sorted by index.
			if choice.FinishReason != nil && len(toolAccum) > 0 {
				msg.ToolCalls = append(msg.ToolCalls, flushToolAccumToolCalls(toolAccum)...)
				toolAccum = nil
			}

			// Send the message if it has content, tool calls, or a finish reason.
			if msg.Content != "" || len(msg.ToolCalls) > 0 || msg.ResponseMeta != nil {
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

// flushToolAccumToolCalls builds a sorted slice of ToolCalls from a tool-call
// accumulator map. The map keys are call indices; the result is sorted by
// ascending index.
func flushToolAccumToolCalls(acc map[int]*toolCallAccumState) []schema.ToolCall {
	indices := make([]int, 0, len(acc))
	for i := range acc {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	out := make([]schema.ToolCall, 0, len(indices))
	for _, i := range indices {
		st := acc[i]
		idx := i
		out = append(out, schema.ToolCall{
			Index: &idx,
			ID:    st.id,
			Function: schema.FunctionCall{
				Name:      st.name,
				Arguments: st.args,
			},
		})
	}
	return out
}
