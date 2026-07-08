package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// --- Chat completion types ---

type copilotMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []copilotToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Name       string            `json:"name,omitempty"`

	ReasoningText   string `json:"reasoning_text,omitempty"`
	ReasoningOpaque string `json:"reasoning_opaque,omitempty"`
}

type copilotToolCall struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function copilotToolCallFunc `json:"function"`
}

type copilotToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type copilotToolDef struct {
	Type     string            `json:"type"`
	Function copilotToolDefFunc `json:"function"`
}

type copilotToolDefFunc struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  copilotToolParams `json:"parameters"`
}

type copilotToolParams struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

type copilotChatRequest struct {
	Model               string           `json:"model"`
	Messages            []copilotMessage `json:"messages"`
	Temperature         *float32         `json:"temperature,omitempty"`
	MaxCompletionTokens *int             `json:"max_completion_tokens,omitempty"`
	ReasoningEffort     ReasoningEffort  `json:"reasoning_effort,omitempty"`
	Stream              bool             `json:"stream"`
	Tools               []copilotToolDef `json:"tools,omitempty"`
}

type copilotChatResponse struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []copilotChatChoice `json:"choices"`
	Usage   *copilotUsage       `json:"usage,omitempty"`
	Error   *copilotAPIError    `json:"error,omitempty"`
}

type copilotChatChoice struct {
	Index        int            `json:"index"`
	Message      copilotMessage `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

type copilotChatChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Choices []copilotChunkChoice `json:"choices"`
	Usage   *copilotUsage        `json:"usage,omitempty"`
}

type copilotChunkChoice struct {
	Index        int          `json:"index"`
	Delta        copilotDelta `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
}

type copilotDelta struct {
	Role            string                  `json:"role,omitempty"`
	Content         string                  `json:"content,omitempty"`
	ToolCalls       []copilotStreamToolCall `json:"tool_calls,omitempty"`
	ReasoningText   string                  `json:"reasoning_text,omitempty"`
	ReasoningOpaque string                  `json:"reasoning_opaque,omitempty"`
}

type copilotStreamToolCall struct {
	Index    *int              `json:"index"`
	ID       string            `json:"id,omitempty"`
	Type     string            `json:"type,omitempty"`
	Function copilotStreamFunc `json:"function,omitempty"`
}

type copilotStreamFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type copilotUsage struct {
	PromptTokens            int                       `json:"prompt_tokens"`
	CompletionTokens        int                       `json:"completion_tokens"`
	TotalTokens             int                       `json:"total_tokens"`
	CompletionTokensDetails *copilotCompletionDetails `json:"completion_tokens_details,omitempty"`
}

type copilotCompletionDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type copilotAPIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func (e *copilotAPIError) Error() string {
	return fmt.Sprintf("copilot API error: %s (type=%s code=%s)", e.Message, e.Type, e.Code)
}

// --- Message conversion (eino ↔ Copilot API) ---

func convertMessages(in []*schema.Message) []copilotMessage {
	out := make([]copilotMessage, 0, len(in))
	for _, msg := range in {
		out = append(out, convertMessage(msg))
	}
	return out
}

func convertMessage(msg *schema.Message) copilotMessage {
	m := copilotMessage{Role: roleString(msg.Role)}

	switch msg.Role {
	case schema.Tool:
		m.ToolCallID = msg.ToolCallID
		m.Content = msg.Content
	case schema.Assistant:
		m.Content = msg.Content
		for _, tc := range msg.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, copilotToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: copilotToolCallFunc{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}
	default:
		m.Content = msg.Content
	}
	return m
}

func roleString(role schema.RoleType) string {
	switch role {
	case schema.User:
		return "user"
	case schema.Assistant:
		return "assistant"
	case schema.System:
		return "system"
	case schema.Tool:
		return "tool"
	default:
		return "user"
	}
}

func convertChoiceToMessage(choice copilotChatChoice) *schema.Message {
	out := &schema.Message{Role: schema.Assistant}
	msg := choice.Message

	if msg.Content != "" {
		out.Content = msg.Content
	}
	if msg.ReasoningText != "" {
		out.ReasoningContent = msg.ReasoningText
	}
	for _, tc := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, schema.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: schema.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return out
}

// --- Tool conversion ---

func convertTools(tools []*schema.ToolInfo) []copilotToolDef {
	if len(tools) == 0 {
		return nil
	}
	out := make([]copilotToolDef, 0, len(tools))
	for _, t := range tools {
		props, required := extractToolParams(t)
		out = append(out, copilotToolDef{
			Type: "function",
			Function: copilotToolDefFunc{
				Name:        t.Name,
				Description: t.Desc,
				Parameters: copilotToolParams{
					Type:       "object",
					Properties: props,
					Required:   required,
				},
			},
		})
	}
	return out
}

// extractToolParams returns properties and required fields from a ToolInfo's
// ParamsOneOf via a single json marshal/unmarshal round-trip.
func extractToolParams(t *schema.ToolInfo) (map[string]interface{}, []string) {
	if t.ParamsOneOf == nil {
		return nil, nil
	}
	raw, err := json.Marshal(t.ParamsOneOf)
	if err != nil {
		return nil, nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil
	}

	var props map[string]interface{}
	if p, ok := m["properties"]; ok {
		props, _ = p.(map[string]interface{})
	}

	var required []string
	if r, ok := m["required"]; ok {
		if reqList, ok := r.([]interface{}); ok {
			required = make([]string, 0, len(reqList))
			for _, ri := range reqList {
				if s, ok := ri.(string); ok {
					required = append(required, s)
				}
			}
		}
	}
	return props, required
}

// --- Chat request building ---

func (m *CopilotModel) buildChatRequest(in []*schema.Message, stream bool, opts ...model.Option) (copilotChatRequest, error) {
	msgs := convertMessages(in)

	options := model.GetCommonOptions(&model.Options{
		Temperature: m.cfg.Temperature,
		MaxTokens:   m.cfg.MaxCompletionTokens,
		Model:       &m.cfg.Model,
		Tools:       m.tools,
		ToolChoice:  m.toolChoice,
	}, opts...)

	return copilotChatRequest{
		Model:               *options.Model,
		Messages:            msgs,
		Temperature:         options.Temperature,
		MaxCompletionTokens: options.MaxTokens,
		ReasoningEffort:     m.cfg.ReasoningEffort,
		Stream:              stream,
		Tools:               convertTools(options.Tools),
	}, nil
}

func (m *CopilotModel) sendChatRequest(ctx context.Context, body copilotChatRequest) ([]byte, error) {
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

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: request failed")
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to read response body")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("copilot: API returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

func usageToTokenUsage(u *copilotUsage) *schema.TokenUsage {
	t := &schema.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.CompletionTokensDetails != nil {
		t.CompletionTokensDetails.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	return t
}

// --- Generate ---

func (m *CopilotModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	body, err := m.buildChatRequest(in, false, opts...)
	if err != nil {
		return nil, err
	}

	respBody, err := m.sendChatRequest(ctx, body)
	if err != nil {
		return nil, err
	}

	var chatResp copilotChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, errors.Wrap(err, "copilot: failed to decode response")
	}
	if chatResp.Error != nil {
		return nil, chatResp.Error
	}
	if len(chatResp.Choices) == 0 {
		return nil, errors.New("copilot: no choices in response")
	}

	msg := convertChoiceToMessage(chatResp.Choices[0])
	if chatResp.Usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{
			FinishReason: chatResp.Choices[0].FinishReason,
			Usage:        usageToTokenUsage(chatResp.Usage),
		}
	}
	return msg, nil
}

// --- Auth headers ---

func setAuthHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Copilot-Integration-ID", "vscode-chat")
	req.Header.Set("Editor-Version", "vscode/1.100.0")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.52.0")
	req.Header.Set("User-Agent", userAgentHeader)
	req.Header.Set("Openai-Intent", "conversation-agent")
}
