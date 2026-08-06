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

// streamResponses streams from the Copilot /responses endpoint (GPT-5-class
// models). This handles SSE events for message content, function calls, and
// reasoning. Built-in provider tool events (web_search, code_interpreter, etc.)
// are tolerated but ignored.
func (m *CopilotModel) streamResponses(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	body, err := m.buildResponsesRequest(in, opts...)
	if err != nil {
		return nil, err
	}
	body.Stream = true

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to marshal responses stream request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to create responses stream request")
	}
	req.Header.Set("Content-Type", "application/json")
	setAuthHeaders(req, m.lockedToken.get())
	setPerRequestHeaders(req, in)
	m.setCommonRequestHeaders(req)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: responses stream request failed")
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		bodyPreview := ""
		if readErr == nil {
			bodyPreview = redactErrorBody(bodyBytes)
		}
		return nil, errors.Errorf("copilot: responses stream returned status %d: %s", resp.StatusCode, bodyPreview)
	}

	sr, sw := schema.Pipe[*schema.Message](1)

	go func() {
		//nolint:errcheck // goroutine close, error is irrelevant
		defer resp.Body.Close()
		defer sw.Close()

		if err := streamResponsesEvents(ctx, resp.Body, sw); err != nil {
			sw.Send(nil, err)
		}
	}()

	return sr, nil
}

// responsesSSEEvent is a single SSE event from the Responses streaming API.
type responsesSSEEvent struct {
	Type     string                `json:"type"`
	Item     *responsesSSEItem     `json:"item,omitempty"`
	Delta    string                `json:"delta,omitempty"`
	Response *responsesSSEResponse `json:"response,omitempty"`
}

type responsesSSEItem struct {
	ID               string                 `json:"id"`
	Type             string                 `json:"type"`
	Role             string                 `json:"role,omitempty"`
	Name             string                 `json:"name,omitempty"`
	CallID           string                 `json:"call_id,omitempty"`
	Arguments        string                 `json:"arguments,omitempty"`
	EncryptedContent string                 `json:"encrypted_content,omitempty"`
	Summary          []responsesSummaryPart `json:"summary,omitempty"`
	Content          []responsesContentPart `json:"content,omitempty"`
}

type responsesSSEResponse struct {
	ID                string                      `json:"id"`
	Usage             *responsesUsage             `json:"usage,omitempty"`
	IncompleteDetails *responsesIncompleteDetails `json:"incomplete_details,omitempty"`
}

func streamResponsesEvents(ctx context.Context, body io.Reader, sw *schema.StreamWriter[*schema.Message]) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	// Accumulate function call arguments across streaming deltas, keyed by item ID.
	var funcArgsAccum map[string]*funcCallAccum

	// Track the overall response metadata.
	var responseID string
	var responseUsage *responsesUsage
	var incompleteReason string

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

		var evt responsesSSEEvent
		if err := json.Unmarshal([]byte(data), &evt); err != nil {
			// Tolerate unknown event shapes gracefully.
			continue
		}

		switch evt.Type {
		case "response.created":
			if evt.Response != nil {
				responseID = evt.Response.ID
			}

		case "response.output_item.added":
			if evt.Item == nil {
				continue
			}
			switch evt.Item.Type {
			case "function_call":
				if funcArgsAccum == nil {
					funcArgsAccum = make(map[string]*funcCallAccum)
				}
				funcArgsAccum[evt.Item.ID] = &funcCallAccum{
					id:   evt.Item.ID,
					name: evt.Item.Name,
				}
			case "reasoning":
				msg := &schema.Message{Role: schema.Assistant}
				if evt.Item.EncryptedContent != "" || evt.Item.ID != "" {
					if msg.Extra == nil {
						msg.Extra = make(map[string]any)
					}
					msg.Extra["copilot_reasoning_item_id"] = evt.Item.ID
					msg.Extra["copilot_encrypted_content"] = evt.Item.EncryptedContent
				}
				for _, s := range evt.Item.Summary {
					if s.Type == "summary_text" && s.Text != "" {
						msg.ReasoningContent += s.Text
					}
				}
				if msg.ReasoningContent != "" || msg.Extra != nil {
					sw.Send(msg, nil)
				}
			}

		case "response.output_text.delta":
			msg := &schema.Message{Role: schema.Assistant}
			if evt.Delta != "" {
				msg.Content = evt.Delta
				sw.Send(msg, nil)
			}

		case "response.function_call_arguments.delta":
			if evt.Item == nil || evt.Item.ID == "" {
				continue
			}
			if acc, ok := funcArgsAccum[evt.Item.ID]; ok {
				if acc.callID == "" && evt.Item.CallID != "" {
					acc.callID = evt.Item.CallID
				}
				acc.args += evt.Delta
			}

		case "response.reasoning_summary_text.delta":
			msg := &schema.Message{Role: schema.Assistant}
			if evt.Delta != "" {
				msg.ReasoningContent = evt.Delta
			}
			if evt.Item != nil {
				if evt.Item.EncryptedContent != "" || evt.Item.ID != "" {
					if msg.Extra == nil {
						msg.Extra = make(map[string]any)
					}
					msg.Extra["copilot_reasoning_item_id"] = evt.Item.ID
					msg.Extra["copilot_encrypted_content"] = evt.Item.EncryptedContent
				}
			}
			if msg.ReasoningContent != "" || msg.Extra != nil {
				sw.Send(msg, nil)
			}

		case "response.output_item.done":
			if evt.Item == nil {
				continue
			}
			switch evt.Item.Type {
			case "function_call":
				// Emit the accumulated tool call.
				if acc, ok := funcArgsAccum[evt.Item.ID]; ok {
					tc := schema.ToolCall{
						ID:   acc.callID,
						Type: "function",
						Function: schema.FunctionCall{
							Name:      acc.name,
							Arguments: acc.args,
						},
					}
					if acc.id != "" {
						tc.Extra = map[string]any{"copilot_item_id": acc.id}
					}
					msg := &schema.Message{
						Role:      schema.Assistant,
						ToolCalls: []schema.ToolCall{tc},
					}
					sw.Send(msg, nil)
					delete(funcArgsAccum, evt.Item.ID)
				}
			case "message":
				// Content already emitted via output_text.delta.
				if evt.Item.ID != "" {
					sw.Send(&schema.Message{
						Role:  schema.Assistant,
						Extra: map[string]any{"copilot_item_id": evt.Item.ID},
					}, nil)
				}
			case "reasoning":
				// Already emitted via output_item.added or summary_text.delta.
			}

		case "response.completed", "response.incomplete":
			if evt.Response != nil {
				responseUsage = evt.Response.Usage
				if evt.Response.IncompleteDetails != nil {
					incompleteReason = evt.Response.IncompleteDetails.Reason
				}
			}
			// Emit final metadata message.
			meta := &schema.Message{
				Role: schema.Assistant,
				ResponseMeta: &schema.ResponseMeta{
					FinishReason: responsesFinishReason(incompleteReason),
				},
			}
			if responseUsage != nil {
				meta.ResponseMeta.Usage = responsesUsageToTokenUsage(responseUsage)
			}
			if responseID != "" {
				if meta.Extra == nil {
					meta.Extra = make(map[string]any)
				}
				meta.Extra["copilot_response_id"] = responseID
			}
			sw.Send(meta, nil)
		}

		// Tolerate unknown event types silently (e.g., web_search_call.*,
		// code_interpreter_call.*, image_generation_call.*, etc.).
	}

	if err := scanner.Err(); err != nil {
		return errors.Wrap(err, "copilot: responses stream scanner error")
	}

	// Flush any remaining function-call accumulators that were never closed
	// by a response.output_item.done event (e.g. truncated stream).
	for id, acc := range funcArgsAccum {
		tc := schema.ToolCall{
			ID:   acc.callID,
			Type: "function",
			Function: schema.FunctionCall{
				Name:      acc.name,
				Arguments: acc.args,
			},
		}
		if acc.id != "" {
			tc.Extra = map[string]any{"copilot_item_id": acc.id}
		}
		sw.Send(&schema.Message{
			Role:      schema.Assistant,
			ToolCalls: []schema.ToolCall{tc},
		}, nil)
		delete(funcArgsAccum, id)
	}

	return nil
}

type funcCallAccum struct {
	id     string
	callID string
	name   string
	args   string
}

// responsesFinishReason maps incomplete_details.reason to a normalized finish reason.
func responsesFinishReason(reason string) string {
	switch reason {
	case "":
		return "stop"
	case "max_output_tokens":
		return "length"
	case "content_filter":
		return "content_filter"
	default:
		return reason
	}
}
