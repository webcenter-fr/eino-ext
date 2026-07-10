package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const (
	// OpenAIIntent is the value sent in the Openai-Intent header. kilocode uses
	// "conversation-edits"; we match that unless there is a known reason otherwise.
	OpenAIIntent = "conversation-edits"
)

// CopilotOptions holds per-call implementation-specific options for the
// Copilot chat model. Use model.WrapImplSpecificOptFn to pass these at
// call time.
type CopilotOptions struct {
	// ReasoningEffort overrides Config.ReasoningEffort for this call.
	// When empty, the Config default is used.
	ReasoningEffort ReasoningEffort
}

// --- Chat completion types ---

// copilotMessage mirrors the OpenAI-compatible chat message shape. Content is
// any to support both plain strings and array content (for vision/image_url parts).
type copilotMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content,omitempty"`

	ToolCalls  []copilotToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Name       string            `json:"name,omitempty"`

	ReasoningText   string `json:"reasoning_text,omitempty"`
	ReasoningOpaque string `json:"reasoning_opaque,omitempty"`
}

// copilotContentPart is a single part in an array-content Copilot message.
type copilotContentPart struct {
	Type     string               `json:"type"`
	Text     string               `json:"text,omitempty"`
	ImageURL *copilotImageURLPart `json:"image_url,omitempty"`
}

// copilotImageURLPart holds the URL for an image part.
type copilotImageURLPart struct {
	URL string `json:"url"`
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
	Type     string             `json:"type"`
	Function copilotToolDefFunc `json:"function"`
}

type copilotToolDefFunc struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  copilotToolParams `json:"parameters"`
}

type copilotToolParams struct {
	Type        string                 `json:"type"`
	Properties  map[string]interface{} `json:"properties,omitempty"`
	Required    []string               `json:"required,omitempty"`
	Definitions map[string]interface{} `json:"$defs,omitempty"`
}

type copilotStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type copilotChatRequest struct {
	Model           string                `json:"model"`
	Messages        []copilotMessage      `json:"messages"`
	Temperature     *float32              `json:"temperature,omitempty"`
	MaxTokens       *int                  `json:"max_tokens,omitempty"`
	ReasoningEffort ReasoningEffort       `json:"reasoning_effort,omitempty"`
	TopP            *float32              `json:"top_p,omitempty"`
	Stop            []string              `json:"stop,omitempty"`
	Stream          bool                  `json:"stream"`
	StreamOptions   *copilotStreamOptions `json:"stream_options,omitempty"`
	Tools           []copilotToolDef      `json:"tools,omitempty"`
	ToolChoice      any                   `json:"tool_choice,omitempty"`
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
	PromptTokensDetails     *copilotPromptDetails     `json:"prompt_tokens_details,omitempty"`
}

type copilotCompletionDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type copilotPromptDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type copilotAPIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func (e *copilotAPIError) Error() string {
	return fmt.Sprintf("copilot API error: %s (type=%s code=%s)", e.Message, e.Type, e.Code)
}

// --- Finish reason mapping ---

// mapFinishReason maps Copilot/OpenAI-compatible finish reasons to normalized
// values. Ported from kilocode map-openai-compatible-finish-reason.ts.
func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "stop"
	case "length":
		return "length"
	case "content_filter":
		return "content_filter"
	case "function_call", "tool_calls":
		return "tool_calls"
	default:
		return reason
	}
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
		// Reasoning round-trip: emit reasoning_text when present.
		if msg.ReasoningContent != "" {
			m.ReasoningText = msg.ReasoningContent
		}
		// Reasoning round-trip: emit reasoning_opaque from Extra if present.
		if opaque, ok := msg.Extra["copilot_reasoning_opaque"]; ok {
			if s, ok := opaque.(string); ok && s != "" {
				m.ReasoningOpaque = s
			}
		}
	case schema.User:
		// Vision/image input: build array content when multi-content parts exist.
		if len(msg.UserInputMultiContent) > 0 {
			m.Content = buildUserContentParts(msg.UserInputMultiContent)
		} else if len(msg.MultiContent) > 0 {
			m.Content = buildUserContentPartsFromDeprecated(msg.MultiContent)
		} else {
			m.Content = msg.Content
		}
	case schema.System:
		m.Content = msg.Content
	default:
		m.Content = msg.Content
	}
	return m
}

// buildUserContentParts converts MessageInputPart slices to the Copilot
// array-content format.
func buildUserContentParts(parts []schema.MessageInputPart) []copilotContentPart {
	return buildContentParts(parts)
}

// buildUserContentPartsFromDeprecated converts deprecated MultiContent to
// Copilot array-content format.
func buildUserContentPartsFromDeprecated(parts []schema.ChatMessagePart) []copilotContentPart {
	return buildContentParts(parts)
}

func buildContentParts(parts interface{}) []copilotContentPart {
	var result []copilotContentPart
	switch p := parts.(type) {
	case []schema.MessageInputPart:
		for _, part := range p {
			result = append(result, convertMessageInputPart(part))
		}
	case []schema.ChatMessagePart:
		for _, part := range p {
			result = append(result, convertChatMessagePart(part))
		}
	}
	return result
}

func convertMessageInputPart(part schema.MessageInputPart) copilotContentPart {
	switch part.Type {
	case schema.ChatMessagePartTypeText:
		return copilotContentPart{Type: "text", Text: part.Text}
	case schema.ChatMessagePartTypeImageURL:
		return copilotContentPart{
			Type:     "image_url",
			ImageURL: buildImageURLPart(part.Image),
		}
	default:
		return copilotContentPart{Type: "text", Text: part.Text}
	}
}

func convertChatMessagePart(part schema.ChatMessagePart) copilotContentPart {
	switch part.Type {
	case schema.ChatMessagePartTypeText:
		return copilotContentPart{Type: "text", Text: part.Text}
	case schema.ChatMessagePartTypeImageURL:
		return copilotContentPart{
			Type:     "image_url",
			ImageURL: buildImageURLPartFromDeprecated(part.ImageURL),
		}
	default:
		return copilotContentPart{Type: "text", Text: part.Text}
	}
}

func buildImageURLPart(img *schema.MessageInputImage) *copilotImageURLPart {
	if img == nil {
		return nil
	}
	return &copilotImageURLPart{URL: imageDataToURL(img.URL, img.Base64Data, img.MIMEType)}
}

func buildImageURLPartFromDeprecated(img *schema.ChatMessageImageURL) *copilotImageURLPart {
	if img == nil {
		return nil
	}
	// In the deprecated ChatMessageImageURL, the URL field already holds the
	// final URL (which may be a data: URL). No separate Base64Data field exists.
	if img.URL != "" {
		return &copilotImageURLPart{URL: img.URL}
	}
	return nil
}

// imageDataToURL builds an image URL string. If URL is non-nil, it is used directly.
// Otherwise a data URL is built from Base64Data + MIMEType. When MIMEType is
// "image/*" it normalises to "image/jpeg".
func imageDataToURL(url *string, base64 *string, mimeType string) string {
	if url != nil {
		return *url
	}
	mt := mimeType
	if mt == "image/*" {
		mt = "image/jpeg"
	}
	if base64 != nil {
		return fmt.Sprintf("data:%s;base64,%s", mt, *base64)
	}
	return ""
}

// hasImageParts returns true if any input message carries an image part.
func hasImageParts(in []*schema.Message) bool {
	for _, msg := range in {
		for _, part := range msg.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeImageURL {
				return true
			}
		}
		for _, part := range msg.MultiContent {
			if part.Type == schema.ChatMessagePartTypeImageURL {
				return true
			}
		}
	}
	return false
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

	if s, ok := msg.Content.(string); ok && s != "" {
		out.Content = s
	}
	if msg.ReasoningText != "" {
		out.ReasoningContent = msg.ReasoningText
	}
	// Persist reasoning_opaque into Extra so the next turn can send it back.
	if msg.ReasoningOpaque != "" {
		if out.Extra == nil {
			out.Extra = make(map[string]any)
		}
		out.Extra["copilot_reasoning_opaque"] = msg.ReasoningOpaque
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
		props, required, defs := extractToolParams(t)
		out = append(out, copilotToolDef{
			Type: "function",
			Function: copilotToolDefFunc{
				Name:        t.Name,
				Description: t.Desc,
				Parameters: copilotToolParams{
					Type:        "object",
					Properties:  props,
					Required:    required,
					Definitions: defs,
				},
			},
		})
	}
	return out
}

// extractToolParams returns properties, required, and $defs from a ToolInfo's
// ParamsOneOf by converting it to a JSON Schema first.
func extractToolParams(t *schema.ToolInfo) (map[string]interface{}, []string, map[string]interface{}) {
	if t.ParamsOneOf == nil {
		return nil, nil, nil
	}
	s, err := t.ParamsOneOf.ToJSONSchema()
	if err != nil || s == nil {
		return nil, nil, nil
	}

	var props map[string]interface{}
	if s.Properties != nil && s.Properties.Len() > 0 {
		props = make(map[string]interface{}, s.Properties.Len())
		for pair := s.Properties.Oldest(); pair != nil; pair = pair.Next() {
			props[pair.Key] = pair.Value
		}
	}

	var defs map[string]interface{}
	if len(s.Definitions) > 0 {
		defs = make(map[string]interface{}, len(s.Definitions))
		for k, v := range s.Definitions {
			defs[k] = v
		}
	}

	return props, s.Required, defs
}

// --- Tool choice conversion ---

// convertToolChoice maps eino schema.ToolChoice to the Copilot API value.
func convertToolChoice(tc *schema.ToolChoice, allowedToolNames []string) any {
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
			return map[string]any{
				"type": "function",
				"function": map[string]string{
					"name": allowedToolNames[0],
				},
			}
		}
		return "required"
	default:
		return "auto"
	}
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
		return copilotChatRequest{}, errors.New("copilot: model must not be empty; set Config.Model or pass model.WithModel()")
	}

	req := copilotChatRequest{
		Model:           resolvedModel,
		Messages:        msgs,
		Temperature:     options.Temperature,
		MaxTokens:       options.MaxTokens,
		TopP:            options.TopP,
		Stop:            options.Stop,
		ReasoningEffort: effort,
		Stream:          stream,
		Tools:           convertTools(options.Tools),
	}
	if stream {
		req.StreamOptions = &copilotStreamOptions{IncludeUsage: true}
	}

	// Only send tool_choice when tools are present.
	if len(req.Tools) > 0 {
		req.ToolChoice = convertToolChoice(options.ToolChoice, options.AllowedToolNames)
	}

	return req, nil
}

func (m *CopilotModel) sendChatRequest(ctx context.Context, body copilotChatRequest, in []*schema.Message) ([]byte, error) {
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
		return nil, errors.Errorf("copilot: API returned status %d: %s", resp.StatusCode, redactErrorBody(respBody))
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
	if u.PromptTokensDetails != nil {
		t.PromptTokenDetails.CachedTokens = u.PromptTokensDetails.CachedTokens
	}
	return t
}

// --- Generate ---

func (m *CopilotModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	// GPT-5 routing: when the resolved model needs the Responses API, dispatch there.
	resolvedModel := m.resolveModel(opts...)
	if m.useResponsesAPI(resolvedModel) {
		return m.generateResponses(ctx, in, opts...)
	}

	body, err := m.buildChatRequest(in, false, opts...)
	if err != nil {
		return nil, err
	}

	respBody, err := m.sendChatRequest(ctx, body, in)
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
	msg.ResponseMeta = &schema.ResponseMeta{
		FinishReason: mapFinishReason(chatResp.Choices[0].FinishReason),
	}
	if chatResp.Usage != nil {
		msg.ResponseMeta.Usage = usageToTokenUsage(chatResp.Usage)
	}
	return msg, nil
}

// resolveModel returns the effective model ID after applying Config and per-call options.
func (m *CopilotModel) resolveModel(opts ...model.Option) string {
	options := model.GetCommonOptions(&model.Options{
		Model: &m.cfg.Model,
	}, opts...)
	if options.Model != nil {
		return *options.Model
	}
	return m.cfg.Model
}

// --- Auth headers ---

// redactErrorBody truncates the response body to at most 500 characters to
// avoid leaking sensitive data in error messages while retaining enough
// context for debugging.
func redactErrorBody(body []byte) string {
	const maxLen = 500
	if len(body) > maxLen {
		return string(body[:maxLen]) + "..."
	}
	return string(body)
}

func setAuthHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Copilot-Integration-ID", "vscode-chat")
	req.Header.Set("Editor-Version", "vscode/1.100.0")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.52.0")
	req.Header.Set("User-Agent", userAgentHeader)
	req.Header.Set("Openai-Intent", OpenAIIntent)
}

// setPerRequestHeaders adds dynamic headers: x-initiator and Copilot-Vision-Request.
func setPerRequestHeaders(req *http.Request, in []*schema.Message) {
	// x-initiator: "user" when the last message is a plain user text prompt,
	// "agent" otherwise (tool/continuation/assistant follow-up, tool results,
	// synthetic attachment).
	req.Header.Set("x-initiator", xInitiator(in))

	// Copilot-Vision-Request: true when any message carries an image part.
	if hasImageParts(in) {
		req.Header.Set("Copilot-Vision-Request", "true")
	}
}

// xInitiator returns the x-initiator value for the given messages.
func xInitiator(in []*schema.Message) string {
	if len(in) == 0 {
		return "user"
	}
	last := in[len(in)-1]
	// Assistant/tool roles: agent-initiated (follow-up).
	if last.Role == schema.Assistant || last.Role == schema.Tool {
		return "agent"
	}
	// User role: "user" only when it's a plain text prompt (not a synthetic
	// attachment or tool result).
	if last.Role == schema.User {
		// If the message has image parts, it's a user-attached image — still "user".
		return "user"
	}
	return "agent"
}

// --- GPT-5 Responses API routing ---

// gpt5ModelPattern matches GPT-N model IDs where N >= 5.
// Group 1 = major version, Group 2 = separator (., -, or end-of-string).
// Dotted versions (gpt-5.4-nano) are excluded from Responses API routing.
var gpt5ModelPattern = regexp.MustCompile(`^gpt-(\d+)([.\-]|$)`)

// useResponsesAPI returns true when the model should use the Copilot Responses
// API endpoint (/responses) instead of /chat/completions. GPT-5-class models
// (gpt-5, gpt-6, etc.) use Responses, except gpt-5-mini which stays on chat.
// Dotted versions (gpt-5.4-nano, gpt-6.1) are always routed to chat completions.
// When m.cfg.ForceChatCompletions is true, returns false unconditionally.
// Ported from kilocode shouldUseResponsesApi / shouldUseResponses.
func (m *CopilotModel) useResponsesAPI(modelID string) bool {
	if m.cfg.ForceChatCompletions {
		return false
	}
	match := gpt5ModelPattern.FindStringSubmatch(modelID)
	if match == nil {
		return false
	}
	// gpt-5-mini is excluded from Responses routing.
	if modelID == "gpt-5-mini" {
		return false
	}
	// Dotted versions (gpt-5.4-nano) use chat completions, not Responses.
	if match[2] == "." {
		return false
	}
	// gpt-N with N >= 5 uses Responses.
	var n int
	if _, err := fmt.Sscanf(match[1], "%d", &n); err == nil && n >= 5 {
		return true
	}
	return false
}

// generateResponses is the non-streaming Responses API path for GPT-5-class models.
// Implemented in copilot_responses.go.
func (m *CopilotModel) generateResponses(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	body, err := m.buildResponsesRequest(in, opts...)
	if err != nil {
		return nil, err
	}
	return m.sendResponsesRequest(ctx, body, in)
}
