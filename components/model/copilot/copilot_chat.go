package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const (
	// OpenAIIntent is the Copilot intent value for conversation-agent requests.
	OpenAIIntent = "conversation-agent"
)

const copilotOpenAIIntent = OpenAIIntent

// CopilotOptions holds per-call implementation-specific options for the
// Copilot chat model. Use model.WrapImplSpecificOptFn to pass these at
// call time.
//
//nolint:revive // CopilotOptions is the established public name.
type CopilotOptions struct {
	// ReasoningEffort overrides Config.ReasoningEffort for this call.
	// When empty, the Config default is used.
	ReasoningEffort ReasoningEffort

	// Chat completion fields that override Config defaults.
	FrequencyPenalty *float32
	PresencePenalty  *float32
	Seed             *int
	Store            *bool
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

	// Fields backported from kilocode openai-transformer bodyFields.
	Store            *bool    `json:"store,omitempty"`
	FrequencyPenalty *float32 `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float32 `json:"presence_penalty,omitempty"`
	Seed             *int     `json:"seed,omitempty"`
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
		if opaque, ok := msg.Extra[extraKeyReasoningOpaque]; ok {
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
//
//nolint:staticcheck // uses deprecated ChatMessagePart for backward compatibility
func buildUserContentPartsFromDeprecated(parts []schema.ChatMessagePart) []copilotContentPart {
	return buildContentParts(parts)
}

//nolint:staticcheck // uses deprecated ChatMessagePart for backward compatibility
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

//nolint:staticcheck // uses deprecated ChatMessagePart for backward compatibility
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

//nolint:staticcheck // uses deprecated ChatMessageImageURL for backward compatibility
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
		out.Extra[extraKeyReasoningOpaque] = msg.ReasoningOpaque
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
	store := m.cfg.Store
	freqPen := m.cfg.FrequencyPenalty
	presPen := m.cfg.PresencePenalty
	seed := m.cfg.Seed
	if copilotOpts := model.GetImplSpecificOptions[CopilotOptions](nil, opts...); copilotOpts != nil {
		if copilotOpts.ReasoningEffort != "" {
			effort = copilotOpts.ReasoningEffort
		}
		if copilotOpts.Store != nil {
			store = copilotOpts.Store
		}
		if copilotOpts.FrequencyPenalty != nil {
			freqPen = copilotOpts.FrequencyPenalty
		}
		if copilotOpts.PresencePenalty != nil {
			presPen = copilotOpts.PresencePenalty
		}
		if copilotOpts.Seed != nil {
			seed = copilotOpts.Seed
		}
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
		Model:            resolvedModel,
		Messages:         msgs,
		MaxTokens:        options.MaxTokens,
		TopP:             options.TopP,
		Stop:             options.Stop,
		ReasoningEffort:  effort,
		Stream:           stream,
		Tools:            convertTools(options.Tools),
		Store:            store,
		FrequencyPenalty: freqPen,
		PresencePenalty:  presPen,
		Seed:             seed,
	}

	if !isReasoningModel(resolvedModel) {
		req.Temperature = options.Temperature
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

	m.logger.Debugf("copilot: sending %d-byte chat request (model=%s, msgs=%d, tools=%d)", len(payload), body.Model, len(body.Messages), len(body.Tools))

	// Retry transient "model not available for integrator" errors caused by
	// Copilot API backend desync. Up to 2 retries with exponential backoff.
	const maxRetries = 2
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 2 * time.Second
			m.logger.Warnf("copilot: retrying chat request for model %q (attempt %d/%d, backoff %v)", body.Model, attempt, maxRetries, backoff)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		respBody, err := m.sendChatRequestOnce(ctx, payload, body, in)
		if err == nil {
			return respBody, nil
		}

		// Only retry on transient "model not available" errors (API backend desync).
		if !isModelNotAvailableError(err) {
			return nil, err
		}
		m.logger.Warnf("copilot: model %q not available on attempt %d: %v", body.Model, attempt, err)
		// Force a fresh TCP connection on retry: the shared http.Client keeps
		// persistent connections alive, so without this, a retry can be routed
		// to the exact same misbehaving Copilot backend over the same
		// connection, making the retry pointless. Closing idle connections
		// forces the next dial to potentially land on a different backend.
		m.httpClient.CloseIdleConnections()
	}

	return nil, errors.Errorf("copilot: model %q not available after %d attempts", body.Model, maxRetries+1)
}

// sendChatRequestOnce executes a single chat completions HTTP call without retries.
func (m *CopilotModel) sendChatRequestOnce(ctx context.Context, payload []byte, body copilotChatRequest, in []*schema.Message) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")
	setAuthHeaders(req, m.lockedToken.get())
	setPerRequestHeaders(req, in)
	m.setCommonRequestHeaders(req)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "copilot: failed to read response body")
	}

	if resp.StatusCode != http.StatusOK {
		bodyPreview := redactErrorBody(respBody)
		m.logger.Errorf("copilot: %d-byte chat request failed with %d: %s (model=%s, msgs=%d, tools=%d)", len(payload), resp.StatusCode, bodyPreview, body.Model, len(body.Messages), len(body.Tools))
		return nil, errors.Errorf("copilot: API returned status %d: %s", resp.StatusCode, bodyPreview)
	}

	return respBody, nil
}

// isModelNotAvailableError reports whether err is a transient "model not
// available for integrator" error from the Copilot API, caused by backend
// desync during model rollouts.
func isModelNotAvailableError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "model not available for integrator")
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

// Generate sends chat completion messages and returns a single response message.
func (m *CopilotModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	resolvedModel := m.resolveModel(opts...)

	if IsAutoModel(resolvedModel) {
		selected, err := m.ensureAutoModel(ctx)
		if err != nil {
			return nil, err
		}
		// Inject the resolved model so buildChatRequest/buildResponsesRequest
		// and useResponsesAPI see the concrete ID, not "auto".
		opts = append([]model.Option{model.WithModel(selected)}, opts...)
		resolvedModel = selected
		// Session token was acquired by ensureAutoModel; skip ensureSessionToken.
	} else {
		if err := m.ensureSessionToken(ctx, resolvedModel); err != nil {
			return nil, err
		}
	}

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
	req.Header.Set("User-Agent", userAgentHeader)
	req.Header.Set("Openai-Intent", copilotOpenAIIntent)
	req.Header.Set("Copilot-Integration-Id", integrationID)
	req.Header.Set("Editor-Version", editorVersion)
}

// setPerRequestHeaders adds dynamic headers for each API call.
func setPerRequestHeaders(req *http.Request, in []*schema.Message) {
	req.Header.Set("X-Initiator", xInitiator(in))
	req.Header.Set("X-GitHub-Api-Version", copilotAPIVersion)
	req.Header.Set("X-Interaction-Id", newUUID())

	if hasImageParts(in) {
		req.Header.Set("X-Copilot-Vision-Request", "true")
	}
}

// setCommonRequestHeaders adds per-instance headers (machine ID, session token)
// to every API request.
func (m *CopilotModel) setCommonRequestHeaders(req *http.Request) {
	req.Header.Set("X-Client-Machine-Id", m.clientMachineID)
	if st := m.sessionToken.get(); st != "" {
		req.Header.Set("Copilot-Session-Token", st)
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

// GPT-5 Responses API routing
//
// Endpoint selection: models are routed based on the /models catalog. When the
// catalog is not yet cached, a model-ID heuristic is used as fallback.
//
// Preference order per model:
//  1. /responses — if supported AND the model is GPT-5-class (reasoning).
//  2. /chat/completions — if supported (all models).
//  3. /v1/messages — Claude native (future).
//
// gpt-5.4-nano, gpt-5.4-mini, and gpt-5.5 list only /responses in their
// supported_endpoints — they must use /responses.

// gpt5ModelPattern matches GPT-N model IDs where N >= 5.
var gpt5ModelPattern = regexp.MustCompile(`^gpt-(\d+)`)

// wouldUseResponses reports whether a model ID would be routed to /responses
// based on the model-ID heuristic (fallback when catalog is not cached).
func wouldUseResponses(modelID string) bool {
	match := gpt5ModelPattern.FindStringSubmatch(modelID)
	if match == nil {
		return false
	}
	// gpt-5-mini — both /chat/completions and /responses work; prefer /chat/completions.
	if modelID == "gpt-5-mini" || strings.HasPrefix(modelID, "gpt-5-mini-") {
		return false
	}
	// All other gpt-5+ models → /responses.  gpt-5.4-nano, gpt-5.4-mini, gpt-5.5
	// list only /responses in their catalog and must not be routed to /chat/completions.
	var n int
	if _, err := fmt.Sscanf(match[1], "%d", &n); err == nil && n >= 5 {
		return true
	}
	return false
}

// useResponsesAPI returns true when the model should use the Copilot Responses
// API endpoint (/responses) instead of /chat/completions.
//
// When a model catalog is cached via ListModelsCache, supported_endpoints from
// the catalog drive the decision. Otherwise the model-ID heuristic (wouldUseResponses)
// is used as fallback. ForceChatCompletions overrides both to false.
func (m *CopilotModel) useResponsesAPI(modelID string) bool {
	if m.cfg.ForceChatCompletions {
		if wouldUseResponses(modelID) && m.logger != nil {
			m.logger.Debugf("copilot: ForceChatCompletions=true overriding /responses routing for model %q; using /chat/completions instead", modelID)
		}
		return false
	}

	if cached := m.getCachedModels(); len(cached) > 0 {
		for _, mi := range cached {
			if mi.ID == modelID {
				for _, ep := range mi.SupportedEndpoints {
					if ep == "/responses" {
						return true
					}
				}
				return false
			}
		}
	}

	return wouldUseResponses(modelID)
}

// PopulateModelsCache calls ListModels with the model's current token and base
// URL and stores the result for endpoint-routing decisions. Subsequent calls to
// useResponsesAPI will consult the catalog's supported_endpoints instead of
// falling back to the model-ID heuristic.
//
// Call this once after construction if you want catalog-driven routing.
// The method is idempotent and safe for concurrent use.
func (m *CopilotModel) PopulateModelsCache(ctx context.Context) error {
	models, err := listModelsWithClient(ctx, m.lockedToken.get(), m.baseURL, m.httpClient)
	if err != nil {
		return err
	}
	m.setModelsCache(models)
	return nil
}

// setModelsCache stores a model catalog for routing decisions.
func (m *CopilotModel) setModelsCache(models []ModelInfo) {
	if m.modelsMu == nil {
		return
	}
	m.modelsMu.Lock()
	m.modelsCache = models
	m.modelsMu.Unlock()
}

// getCachedModels returns the cached model catalog or nil.
func (m *CopilotModel) getCachedModels() []ModelInfo {
	if m.modelsMu == nil {
		return nil
	}
	m.modelsMu.RLock()
	defer m.modelsMu.RUnlock()
	return m.modelsCache
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

// isReasoningModel reports whether a model ID corresponds to a reasoning-capable
// model for which temperature is unsupported (any gpt-5*, Claude, Gemini).
// Temperature must be omitted for these models to avoid 400 errors.
func isReasoningModel(modelID string) bool {
	lower := strings.ToLower(modelID)
	if strings.HasPrefix(lower, "gpt-5") {
		return true
	}
	if strings.HasPrefix(lower, "claude-") {
		return true
	}
	if strings.HasPrefix(lower, "gemini-") {
		return true
	}
	return false
}
