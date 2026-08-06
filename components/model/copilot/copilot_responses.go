package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// --- Responses API types ---
//
// These types mirror the Copilot /responses endpoint shape. eino only uses
// function tools; built-in provider tools (web_search, code_interpreter,
// image_generation, file_search, local_shell) and their approval /
// mcp_approval_response flows are intentionally unsupported and excluded
// from these type definitions.

type responsesRequest struct {
	Model          string               `json:"model"`
	Input          []responsesInputItem `json:"input"`
	MaxOutputTokens *int                `json:"max_output_tokens,omitempty"`
	Temperature    *float32             `json:"temperature,omitempty"`
	TopP           *float32             `json:"top_p,omitempty"`
	Stream         bool                 `json:"stream"`
	// GitHub Copilot's /responses endpoint does not support the `store`
	// parameter (it rejects any value with a 400 "store is not supported").
	// Copilot is stateless: reasoning is round-tripped via encrypted_content
	// rather than server-side stored items, so store is always omitted.
	Store          bool                 `json:"store,omitempty"`
	Tools          []responsesTool      `json:"tools,omitempty"`
	ToolChoice     any                  `json:"tool_choice,omitempty"`
	Include        []string             `json:"include,omitempty"`
	Reasoning      *responsesReasoning  `json:"reasoning,omitempty"`
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// responsesInputItem is a union of input item types. Only the ones eino uses are
// represented.
type responsesInputItem struct {
	Type    string                   `json:"type,omitempty"`
	Role    string                   `json:"role,omitempty"`
	Content any                      `json:"content,omitempty"`
	ID      string                   `json:"id,omitempty"`
	CallID  string                   `json:"call_id,omitempty"`
	Name    string                   `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	Output  string                   `json:"output,omitempty"`

	// reasoning fields
	EncryptedContent string            `json:"encrypted_content,omitempty"`
	Summary          []responsesSummaryPart `json:"summary,omitempty"`
}

type responsesSummaryPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesTool struct {
	Type     string             `json:"type"`
	Name     string             `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters json.RawMessage  `json:"parameters,omitempty"`
	Strict   bool               `json:"strict,omitempty"`
}

// responsesOutputItem represents an item in the response output array.
type responsesOutputItem struct {
	Type    string `json:"type"`
	Role    string `json:"role,omitempty"`
	ID      string `json:"id,omitempty"`
	Content []responsesContentPart `json:"content,omitempty"`
	CallID  string `json:"call_id,omitempty"`
	Name    string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	EncryptedContent string            `json:"encrypted_content,omitempty"`
	Summary   []responsesSummaryPart    `json:"summary,omitempty"`
}

type responsesContentPart struct {
	Type      string                   `json:"type"`
	Text      string                   `json:"text,omitempty"`
	LogProbs  interface{}              `json:"logprobs,omitempty"`
	Annotations []interface{}          `json:"annotations,omitempty"`
}

type responsesResponse struct {
	ID         string                `json:"id"`
	CreatedAt  int64                 `json:"created_at"`
	Model      string                `json:"model"`
	Output     []responsesOutputItem `json:"output"`
	Usage      *responsesUsage       `json:"usage,omitempty"`
	IncompleteDetails *responsesIncompleteDetails `json:"incomplete_details,omitempty"`
	Error      *responsesAPIError    `json:"error,omitempty"`
}

type responsesIncompleteDetails struct {
	Reason string `json:"reason"`
}

type responsesUsage struct {
	InputTokens          int                         `json:"input_tokens"`
	OutputTokens         int                         `json:"output_tokens"`
	TotalTokens          int                         `json:"total_tokens"`
	InputTokensDetails   *responsesInputTokDetails   `json:"input_tokens_details,omitempty"`
	OutputTokensDetails  *responsesOutputTokDetails  `json:"output_tokens_details,omitempty"`
}

type responsesInputTokDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type responsesOutputTokDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type responsesAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// --- Responses input conversion ---

// convertToResponsesInput converts eino messages to the Responses API input format.
// systemMessageMode defaults to "system". Built-in provider tools are out of
// scope — only function tools are supported.
func convertToResponsesInput(in []*schema.Message) []responsesInputItem {
	var items []responsesInputItem

	for _, msg := range in {
		switch msg.Role {
		case schema.System:
			items = append(items, responsesInputItem{
				Role:    "system",
				Content: msg.Content,
			})
		case schema.User:
			if len(msg.UserInputMultiContent) > 0 {
				items = append(items, responsesInputItem{
					Role:    "user",
					Content: convertToResponsesUserContent(msg.UserInputMultiContent),
				})
			} else if len(msg.MultiContent) > 0 {
				items = append(items, responsesInputItem{
					Role:    "user",
					Content: convertToResponsesUserContentDeprecated(msg.MultiContent),
				})
			} else {
				items = append(items, responsesInputItem{
					Role: "user",
					Content: []map[string]string{
						{"type": "input_text", "text": msg.Content},
					},
				})
			}
		case schema.Assistant:
			items = append(items, convertAssistantToResponses(msg)...)
		case schema.Tool:
			items = append(items, responsesInputItem{
				Type:   "function_call_output",
				CallID: msg.ToolCallID,
				Output: msg.Content,
			})
		}
	}
	return items
}

func convertToResponsesUserContent(parts []schema.MessageInputPart) []map[string]string {
	var result []map[string]string
	for _, p := range parts {
		switch p.Type {
		case schema.ChatMessagePartTypeText:
			result = append(result, map[string]string{"type": "input_text", "text": p.Text})
		case schema.ChatMessagePartTypeImageURL:
			result = append(result, map[string]string{
				"type":      "input_image",
				"image_url": imageInputURL(p.Image),
			})
		}
	}
	return result
}

//nolint:staticcheck // intentionally handles deprecated schema.ChatMessagePart for backward compatibility
func convertToResponsesUserContentDeprecated(parts []schema.ChatMessagePart) []map[string]string {
	var result []map[string]string
	for _, p := range parts {
		switch p.Type {
		case schema.ChatMessagePartTypeText:
			result = append(result, map[string]string{"type": "input_text", "text": p.Text})
		case schema.ChatMessagePartTypeImageURL:
			if p.ImageURL != nil && p.ImageURL.URL != "" {
				result = append(result, map[string]string{
					"type":      "input_image",
					"image_url": p.ImageURL.URL,
				})
			}
		}
	}
	return result
}

func imageInputURL(img *schema.MessageInputImage) string {
	if img == nil {
		return ""
	}
	if img.URL != nil {
		return *img.URL
	}
	mt := img.MIMEType
	if mt == "image/*" {
		mt = "image/jpeg"
	}
	if img.Base64Data != nil {
		return fmt.Sprintf("data:%s;base64,%s", mt, *img.Base64Data)
	}
	return ""
}

func convertAssistantToResponses(msg *schema.Message) []responsesInputItem {
	var items []responsesInputItem

	// Item ID for round-tripping stored responses.
	var itemID string
	if msg.Extra != nil {
		if id, ok := msg.Extra["copilot_item_id"]; ok {
			if s, ok := id.(string); ok {
				itemID = s
			}
		}
	}

	// Text content.
	if msg.Content != "" {
		item := responsesInputItem{
			Role: "assistant",
			Content: []map[string]string{
				{"type": "output_text", "text": msg.Content},
			},
		}
		if itemID != "" {
			item.ID = itemID
		}
		items = append(items, item)
	}

	// Tool calls.
	for _, tc := range msg.ToolCalls {
		item := responsesInputItem{
			Type:      "function_call",
			CallID:    tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		}
		// Carry item id from Extra if available.
		if tc.Extra != nil {
			if id, ok := tc.Extra["copilot_item_id"]; ok {
				if s, ok := id.(string); ok {
					item.ID = s
				}
			}
		}
		items = append(items, item)
	}

	// Reasoning content — emit as reasoning item when there's a stored item id
	// and encrypted_content.
	if msg.ReasoningContent != "" || msg.Extra != nil {
		var encContent string
		var reasonItemID string
		if msg.Extra != nil {
			if ec, ok := msg.Extra["copilot_encrypted_content"]; ok {
				if s, ok := ec.(string); ok {
					encContent = s
				}
			}
			if id, ok := msg.Extra["copilot_reasoning_item_id"]; ok {
				if s, ok := id.(string); ok {
					reasonItemID = s
				}
			}
		}
		if reasonItemID != "" && encContent != "" {
			summary := []responsesSummaryPart{}
			if msg.ReasoningContent != "" {
				summary = append(summary, responsesSummaryPart{
					Type: "summary_text",
					Text: msg.ReasoningContent,
				})
			}
			items = append(items, responsesInputItem{
				Type:             "reasoning",
				ID:               reasonItemID,
				EncryptedContent: encContent,
				Summary:          summary,
			})
		}
	}

	return items
}

// --- Responses non-streaming request/response ---

func (m *CopilotModel) buildResponsesRequest(in []*schema.Message, opts ...model.Option) (responsesRequest, error) {
	// Resolve per-call options against config defaults, mirroring buildChatRequest.
	options := model.GetCommonOptions(&model.Options{
		MaxTokens:   m.cfg.MaxCompletionTokens,
		Model:       &m.cfg.Model,
		Tools:       m.tools,
		ToolChoice:  m.toolChoice,
		Temperature: m.cfg.Temperature,
	}, opts...)

	// Per-call reasoning effort override via CopilotOptions.
	effort := m.cfg.ReasoningEffort
	if copilotOpts := model.GetImplSpecificOptions[CopilotOptions](nil, opts...); copilotOpts != nil && copilotOpts.ReasoningEffort != "" {
		effort = copilotOpts.ReasoningEffort
	}

	// Validate model: if the resolved model is empty, fail before calling the API.
	resolvedModel := ""
	if options.Model != nil {
		resolvedModel = *options.Model
	}
	if resolvedModel == "" {
		return responsesRequest{}, errors.New("copilot: model must not be empty; set Config.Model or pass model.WithModel()")
	}

	req := responsesRequest{
		Model:           resolvedModel,
		Input:           convertToResponsesInput(in),
		MaxOutputTokens: options.MaxTokens,
		TopP:            options.TopP,
	}

	if !isReasoningModel(resolvedModel) {
		req.Temperature = options.Temperature
	}

	// Reasoning: apply GPT-5 defaults when no explicit effort is configured.
	// Kilocode defaults to effort "medium", summary "auto", and include
	// ["reasoning.encrypted_content"] for all non-chat/non-pro GPT-5 models.
	if shouldSetReasoningDefaults(resolvedModel, effort) {
		req.Include = []string{"reasoning.encrypted_content"}
		req.Reasoning = &responsesReasoning{Effort: string(ReasoningEffortMedium), Summary: "auto"}
	} else if effort != "" {
		req.Include = []string{"reasoning.encrypted_content"}
		req.Reasoning = &responsesReasoning{Effort: string(effort), Summary: "auto"}
	}

	// Tools: use per-call tools and tool_choice, wire the flat Responses format.
	if len(options.Tools) > 0 {
		req.Tools = convertResponsesTools(options.Tools)
		req.ToolChoice = convertResponsesToolChoice(options.ToolChoice, options.AllowedToolNames)
	}

	return req, nil
}

// convertResponsesToolChoice maps eino schema.ToolChoice to the Copilot
// Responses API flat format. Unlike the Chat Completions nested format
// ({type:"function", function:{name:"x"}}), the Responses API expects a
// flat shape: {type:"function", name:"x"}.
func convertResponsesToolChoice(tc *schema.ToolChoice, allowedToolNames []string) any {
	if tc == nil {
		return nil
	}
	switch *tc {
	case schema.ToolChoiceForbidden:
		return "none"
	case schema.ToolChoiceAllowed:
		return "auto"
	case schema.ToolChoiceForced:
		if len(allowedToolNames) == 1 {
			return map[string]string{
				"type": "function",
				"name": allowedToolNames[0],
			}
		}
		return "required"
	default:
		return "auto"
	}
}

// shouldSetReasoningDefaults reports whether the model ID qualifies for
// GPT-5 default reasoning configuration (effort "medium", summary "auto",
// include ["reasoning.encrypted_content"]) when no explicit reasoning
// effort is configured. Backported from kilocode gpt5DefaultOptions.
//
// GPT-5 models receive defaults; gpt-5-chat and gpt-5-pro variants
// do not (they handle reasoning differently).
func shouldSetReasoningDefaults(modelID string, configuredEffort ReasoningEffort) bool {
	id := strings.ToLower(modelID)
	return strings.Contains(id, "gpt-5") &&
		!strings.Contains(id, "gpt-5-chat") &&
		!strings.Contains(id, "gpt-5-pro") &&
		configuredEffort == ""
}

func convertResponsesTools(tools []*schema.ToolInfo) []responsesTool {
	out := make([]responsesTool, 0, len(tools))
	for _, t := range tools {
		paramsJSON, _ := json.Marshal(toolParamsToMap(t))
		out = append(out, responsesTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Desc,
			Parameters:  paramsJSON,
			Strict:      true,
		})
	}
	return out
}

func toolParamsToMap(t *schema.ToolInfo) map[string]interface{} {
	// The Responses API requires additionalProperties:false when strict:true is
	// set on a tool definition. Always include it since convertResponsesTools is
	// the only caller and always sets strict.
	result := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
	}
	if t.ParamsOneOf != nil {
		s, err := t.ParamsOneOf.ToJSONSchema()
		if err == nil && s != nil {
			if s.Properties != nil && s.Properties.Len() > 0 {
				props := make(map[string]interface{}, s.Properties.Len())
				for pair := s.Properties.Oldest(); pair != nil; pair = pair.Next() {
					props[pair.Key] = pair.Value
				}
				result["properties"] = props
			}
			if len(s.Required) > 0 {
				result["required"] = s.Required
			}
		}
	}
	return result
}

func (m *CopilotModel) sendResponsesRequest(ctx context.Context, body responsesRequest, in []*schema.Message) (*schema.Message, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to marshal responses request")
	}

	// Retry transient "model not available for integrator" errors caused by
	// Copilot API backend desync. Up to 2 retries with exponential backoff.
	const maxRetries = 2
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 2 * time.Second
			m.logger.Warnf("copilot: retrying responses request for model %q (attempt %d/%d, backoff %v)", body.Model, attempt, maxRetries, backoff)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		msg, err := m.sendResponsesRequestOnce(ctx, payload, body, in)
		if err == nil {
			return msg, nil
		}

		// Only retry on transient "model not available" errors (API backend desync).
		if !isModelNotAvailableError(err) {
			return nil, err
		}
		m.logger.Warnf("copilot: model %q not available on responses attempt %d: %v", body.Model, attempt, err)
		// Force a fresh TCP connection on retry (see sendChatRequest for rationale).
		m.httpClient.CloseIdleConnections()
	}

	return nil, errors.Errorf("copilot: model %q not available via responses after %d attempts", body.Model, maxRetries+1)
}

func (m *CopilotModel) sendResponsesRequestOnce(ctx context.Context, payload []byte, body responsesRequest, in []*schema.Message) (*schema.Message, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to create responses request")
	}
	req.Header.Set("Content-Type", "application/json")
	setAuthHeaders(req, m.lockedToken.get())
	setPerRequestHeaders(req, in)
	m.setCommonRequestHeaders(req)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: responses request failed")
	}
	//nolint:errcheck // defer close in request path, error is irrelevant
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to read responses body")
	}

	// ... after reading, if we're in the 502 area, this is the responses code path
	if resp.StatusCode != http.StatusOK {
		bodyPreview := redactErrorBody(respBody)
		return nil, errors.Errorf("copilot: responses API returned status %d: %s", resp.StatusCode, bodyPreview)
	}

	var r responsesResponse
	if err := json.Unmarshal(respBody, &r); err != nil {
		return nil, errors.Wrap(err, "copilot: failed to decode responses response")
	}
	if r.Error != nil {
		return nil, errors.Errorf("copilot: responses API error: %s (code=%s)", r.Error.Message, r.Error.Code)
	}

	return parseResponsesOutput(&r)
}

// parseResponsesOutput converts a /responses API response into an eino Message.
func parseResponsesOutput(r *responsesResponse) (*schema.Message, error) {
	msg := &schema.Message{
		Role: schema.Assistant,
		Extra: make(map[string]any),
	}
	if r.ID != "" {
		msg.Extra["copilot_response_id"] = r.ID
	}

	var hasFunctionCall bool
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			for _, cp := range item.Content {
				if cp.Type == "output_text" {
					if msg.Content != "" {
						msg.Content += cp.Text
					} else {
						msg.Content = cp.Text
					}
				}
			}
			if item.ID != "" {
				msg.Extra["copilot_item_id"] = item.ID
			}
		case "function_call":
			hasFunctionCall = true
			tc := schema.ToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			}
			if item.ID != "" {
				tc.Extra = map[string]any{"copilot_item_id": item.ID}
			}
			msg.ToolCalls = append(msg.ToolCalls, tc)
		case "reasoning":
			if item.ID != "" {
				msg.Extra["copilot_reasoning_item_id"] = item.ID
			}
			if item.EncryptedContent != "" {
				msg.Extra["copilot_encrypted_content"] = item.EncryptedContent
			}
			for _, s := range item.Summary {
				if s.Type == "summary_text" {
					if msg.ReasoningContent != "" {
						msg.ReasoningContent += s.Text
					} else {
						msg.ReasoningContent = s.Text
					}
				}
			}
		}
	}

	// Finish reason mapping (ported from map-openai-responses-finish-reason.ts).
	finishReason := "stop"
	if r.IncompleteDetails != nil {
		switch r.IncompleteDetails.Reason {
		case "max_output_tokens":
			finishReason = "length"
		case "content_filter":
			finishReason = "content_filter"
		default:
			finishReason = r.IncompleteDetails.Reason
		}
	}
	if hasFunctionCall && finishReason == "stop" {
		finishReason = "tool_calls"
	}

	msg.ResponseMeta = &schema.ResponseMeta{
		FinishReason: finishReason,
	}
	if r.Usage != nil {
		msg.ResponseMeta.Usage = responsesUsageToTokenUsage(r.Usage)
	}

	return msg, nil
}

func responsesUsageToTokenUsage(u *responsesUsage) *schema.TokenUsage {
	t := &schema.TokenUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.OutputTokensDetails != nil {
		t.CompletionTokensDetails.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	if u.InputTokensDetails != nil {
		t.PromptTokenDetails.CachedTokens = u.InputTokensDetails.CachedTokens
	}
	return t
}
