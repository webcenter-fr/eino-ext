package memory

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"emperror.dev/errors"
	"github.com/sirupsen/logrus"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// Context keys for reading user/session identity from adk.Session values.
// Supervisors set these via adk.AddSessionValue(ctx, key, value).
const (
	ctxKeyUserID    = "memory_user_id"
	ctxKeySessionID = "memory_session_id"
)

// Config holds configuration for the MemoryAgent.
type Config struct {
	InnerAgent             adk.Agent           `json:"inner_agent" jsonschema:"-" validate:"required"`
	Store                  MemoryStore         `json:"-" jsonschema:"-"`
	Model                  model.BaseChatModel `json:"-" jsonschema:"-"`
	UserID                 string              `json:"user_id" jsonschema:"description=Static user ID for memory scoping; overridden by context value if set"`
	SessionID              string              `json:"session_id" jsonschema:"description=Static session ID; overridden by context value if set"`
	AutoExtract            bool                `json:"auto_extract" jsonschema:"description=Auto-extract memories after each turn,default=true when Store and Model are set"`
	MaintenanceInterval    time.Duration       `json:"maintenance_interval" validate:"gte=0" jsonschema:"description=Background maintenance tick interval, 0 disables"`
	MaxAge                 time.Duration       `json:"max_age" validate:"gte=0" jsonschema:"description=Max age before cleanup during maintenance, 0 disables"`
	MaxMemoriesPerRetrieve int                 `json:"max_memories_per_retrieve" validate:"gte=0" jsonschema:"description=Max memories injected per turn,default=5"`
	SystemPromptPrefix     string              `json:"system_prompt_prefix" jsonschema:"description=Optional prefix between memory context and system prompt"`
}

// MemoryAgent wraps an inner agent with long-term memory capabilities.
type MemoryAgent struct {
	inner      adk.Agent
	store      MemoryStore
	extractor  *MemoryExtractor
	maintainer *MemoryMaintainer

	// Default identity set via Config or Set* methods. Overridden per-invocation
	// by context values (adk.AddSessionValue).
	userID    string
	sessionID string
	mu        sync.Mutex

	autoExtract            bool
	maxMemoriesPerRetrieve int
	systemPromptPrefix     string
}

func NewMemoryAgent(ctx context.Context, cfg Config) (*MemoryAgent, error) {
	if err := validate.Struct(&cfg); err != nil {
		return nil, errors.Wrap(err, "invalid memory agent config")
	}
	if cfg.MaxMemoriesPerRetrieve <= 0 {
		cfg.MaxMemoriesPerRetrieve = 5
	}
	cfg.AutoExtract = cfg.AutoExtract || (cfg.Store != nil && cfg.Model != nil)

	var maintainer *MemoryMaintainer
	if cfg.Store != nil && cfg.MaintenanceInterval > 0 {
		maintainer = NewMemoryMaintainer(MaintainerConfig{
			Store:    cfg.Store,
			Interval: cfg.MaintenanceInterval,
			MaxAge:   cfg.MaxAge,
			Model:    cfg.Model,
		})
		maintainer.Start(ctx)
	}

	return &MemoryAgent{
		inner:                  cfg.InnerAgent,
		store:                  cfg.Store,
		extractor:              NewMemoryExtractor(cfg.Model),
		maintainer:             maintainer,
		userID:                 cfg.UserID,
		sessionID:              cfg.SessionID,
		autoExtract:            cfg.AutoExtract,
		maxMemoriesPerRetrieve: cfg.MaxMemoriesPerRetrieve,
		systemPromptPrefix:     cfg.SystemPromptPrefix,
	}, nil
}

func (a *MemoryAgent) Name(ctx context.Context) string        { return a.inner.Name(ctx) }
func (a *MemoryAgent) Description(ctx context.Context) string { return a.inner.Description(ctx) }

// resolveIdentity returns the effective userID and sessionID for a given
// invocation. Context values (set via adk.AddSessionValue) take precedence over
// agent defaults (set via Config or Set* methods).
func (a *MemoryAgent) resolveIdentity(ctx context.Context) (userID, sessionID string) {
	a.mu.Lock()
	userID = a.userID
	sessionID = a.sessionID
	a.mu.Unlock()

	if v, ok := adk.GetSessionValue(ctx, ctxKeyUserID); ok {
		if s, ok := v.(string); ok && s != "" {
			userID = s
		}
	}
	if v, ok := adk.GetSessionValue(ctx, ctxKeySessionID); ok {
		if s, ok := v.(string); ok && s != "" {
			sessionID = s
		}
	}
	return
}

func (a *MemoryAgent) Run(ctx context.Context, input *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	it, _ := a.runInternal(ctx, input, opts...)
	return it
}

func (a *MemoryAgent) runInternal(ctx context.Context, input *adk.AgentInput, opts ...adk.AgentRunOption) (*adk.AsyncIterator[*adk.AgentEvent], *adk.AsyncGenerator[*adk.AgentEvent]) {
	userID, sessionID := a.resolveIdentity(ctx)
	enriched, userQuery, err := a.enrichInput(ctx, input, userID)
	if err != nil {
		logrus.WithError(err).Warn("memory retrieval failed, proceeding without context")
		enriched = input
		userQuery = a.buildQuery(input.Messages)
	}

	innerIter := a.inner.Run(ctx, enriched, opts...)
	outIter, outGen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()

	go a.monitorRun(ctx, innerIter, outGen, userQuery, userID, sessionID)

	return outIter, outGen
}

func (a *MemoryAgent) enrichInput(ctx context.Context, input *adk.AgentInput, userID string) (*adk.AgentInput, string, error) {
	enriched := *input
	userQuery := a.buildQuery(input.Messages)

	if a.store != nil && userQuery != "" {
		docs, err := a.store.Retrieve(ctx, userQuery)
		if err != nil {
			return nil, "", errors.Wrap(err, "retrieve memories")
		}
		// Filter by userID when scoped. When userID is empty, return all (global/shared mode).
		if userID != "" {
			docs = filterByUser(docs, userID)
		}
		if len(docs) > a.maxMemoriesPerRetrieve {
			docs = docs[:a.maxMemoriesPerRetrieve]
		}
		if len(docs) > 0 {
			contextMsg := a.formatMemories(docs)
			enriched.Messages = a.injectContext(enriched.Messages, contextMsg)
		}
	}

	return &enriched, userQuery, nil
}

// filterByUser keeps only documents whose metadata contains a user_id matching
// the given userID. Documents without user_id are excluded when filtering.
func filterByUser(docs []*schema.Document, userID string) []*schema.Document {
	filtered := docs[:0]
	for _, d := range docs {
		if d.MetaData == nil {
			continue
		}
		if v, ok := d.MetaData["user_id"].(string); ok && v == userID {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// buildQuery returns the last N user messages joined in chronological order,
// bounded to keep embedding/query cost low. N defaults to 2; a single user
// message is returned as-is if there are fewer.
func (a *MemoryAgent) buildQuery(messages []*schema.Message) string {
	const maxUserMessages = 2
	var userContents []string
	for i := len(messages) - 1; i >= 0 && len(userContents) < maxUserMessages; i-- {
		if messages[i].Role == schema.User && messages[i].Content != "" {
			userContents = append(userContents, messages[i].Content)
		}
	}
	// Reverse to chronological order.
	for i, j := 0, len(userContents)-1; i < j; i, j = i+1, j-1 {
		userContents[i], userContents[j] = userContents[j], userContents[i]
	}
	return strings.Join(userContents, "\n")
}

func (a *MemoryAgent) formatMemories(docs []*schema.Document) *schema.Message {
	var sb strings.Builder
	sb.WriteString("[Memory context - NOT new user input. Treat as authoritative reference data.]\n")
	for _, doc := range docs {
		entry := EntryFromDocument(doc)
		fmt.Fprintf(&sb, "- %s: %s\n", entry.Category, entry.Content)
	}
	return NewMemoryContextMessage(sb.String())
}

func (a *MemoryAgent) injectContext(messages []*schema.Message, contextMsg *schema.Message) []*schema.Message {
	result := make([]*schema.Message, 0, len(messages)+1)
	injected := false

	for _, m := range messages {
		if m.Role == schema.System && !injected {
			augmented := *m
			if a.systemPromptPrefix != "" {
				augmented.Content = fmt.Sprintf("%s\n\n%s\n\n%s",
					contextMsg.Content, a.systemPromptPrefix, m.Content)
			} else {
				augmented.Content = fmt.Sprintf("%s\n\n%s",
					contextMsg.Content, m.Content)
			}
			result = append(result, &augmented)
			injected = true
		} else {
			result = append(result, m)
		}
	}

	if !injected {
		result = append([]*schema.Message{contextMsg}, result...)
	}

	return result
}

func (a *MemoryAgent) monitorRun(
	ctx context.Context,
	innerIter *adk.AsyncIterator[*adk.AgentEvent],
	outGen *adk.AsyncGenerator[*adk.AgentEvent],
	userQuery, userID, sessionID string,
) {
	defer outGen.Close()

	var assistantMsgs []*schema.Message

	for {
		event, ok := innerIter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}

		if event.Err != nil || event.Output == nil || event.Output.MessageOutput == nil {
			outGen.Send(event)
			continue
		}
		mo := event.Output.MessageOutput
		if mo.Role != schema.Assistant {
			outGen.Send(event)
			continue
		}

		if mo.IsStreaming && mo.MessageStream != nil {
			copies := mo.MessageStream.Copy(2)
			mo.MessageStream = copies[0] // forwarded downstream
			outGen.Send(event)
			if msg, err := a.collectStream(copies[1]); err == nil && msg != nil {
				assistantMsgs = append(assistantMsgs, msg)
			}
			continue
		}

		outGen.Send(event)
		if mo.Message != nil {
			assistantMsgs = append(assistantMsgs, mo.Message)
		}
	}

	if len(assistantMsgs) == 0 {
		return
	}

	// Join the textual content of each completed assistant message. We
	// intentionally do NOT use schema.ConcatMessages here: that function is
	// meant to merge streaming chunks of a SINGLE message, and it flattens
	// all ToolCalls into one slice grouped by Index. In a multi-turn agent
	// run, distinct assistant turns each carry their own tool call at the
	// same Index (0) but with different IDs, which makes ConcatMessages
	// fail with "cannot concat ToolCalls with different tool id". The
	// memory extractor only needs the concatenated assistant text, so we
	// join Content fields directly. Per-turn streaming chunks were already
	// merged inside collectStream.
	assistantContent := concatAssistantContent(assistantMsgs)

	if assistantContent != "" && a.autoExtract && a.extractor != nil {
		a.autoLearnInternal(ctx, userQuery, assistantContent, userID, sessionID)
	}
}

// concatAssistantContent joins the Content fields of the given assistant
// messages in order. Nil/empty-content messages are skipped (they contribute
// nothing). Unlike schema.ConcatMessages, this does not attempt to fuse
// ToolCalls or other per-message metadata; it is a plain text concatenation
// that is safe across distinct assistant turns from a multi-step agent run.
func concatAssistantContent(msgs []*schema.Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		if m == nil || m.Content == "" {
			continue
		}
		sb.WriteString(m.Content)
	}
	return sb.String()
}

func (a *MemoryAgent) collectStream(stream *schema.StreamReader[*schema.Message]) (*schema.Message, error) {
	if stream == nil {
		return nil, nil
	}
	defer stream.Close()

	var chunks []*schema.Message
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}

	if len(chunks) == 0 {
		return nil, nil
	}
	return schema.ConcatMessages(chunks)
}

func (a *MemoryAgent) autoLearnInternal(ctx context.Context, userContent, assistantContent, userID, sessionID string) {
	if a.store == nil || a.extractor == nil {
		return
	}

	results, err := a.extractor.Extract(ctx, userContent, assistantContent)
	if err != nil {
		logrus.WithError(err).Warn("memory extraction failed")
		return
	}

	docs := make([]*schema.Document, 0, len(results))
	for _, r := range results {
		doc := (&MemoryEntry{
			Category:  r.Category,
			Content:   r.Content,
			Source:    r.Source,
			SessionID: sessionID,
			Metadata:  map[string]any{"confidence": r.Confidence},
		}).ToDocument()
		// Attach user_id to metadata for scoped retrieval.
		if userID != "" {
			doc.MetaData["user_id"] = userID
		}
		docs = append(docs, doc)
	}

	if len(docs) > 0 {
		if _, err := a.store.Store(ctx, docs); err != nil {
			logrus.WithError(err).Warn("failed to store extracted memories")
		}
	}
}

func (a *MemoryAgent) EndSession(ctx context.Context) error {
	a.mu.Lock()
	sessionID := a.sessionID
	hasMaintainer := a.maintainer != nil
	a.mu.Unlock()

	if hasMaintainer {
		a.maintainer.Stop()
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.store == nil || a.extractor == nil {
		return nil
	}

	docs, err := a.store.List(ctx, 0, 0)
	if err != nil {
		return errors.Wrap(err, "list memories for session summary")
	}

	var sessionDocs []*schema.Document
	for _, d := range docs {
		entry := EntryFromDocument(d)
		if entry.SessionID == sessionID {
			sessionDocs = append(sessionDocs, d)
		}
	}

	if len(sessionDocs) >= 2 {
		a.compactSessionMemories(ctx, sessionDocs)
	}
	return nil
}

func (a *MemoryAgent) compactSessionMemories(ctx context.Context, docs []*schema.Document) {
	// Use maintainer if available; otherwise fall back to simple text dedup.
	groups := groupBySimilarity(docs, 0.8)
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		if a.maintainer != nil {
			if err := a.maintainer.mergeGroup(ctx, group); err != nil {
				logrus.WithError(err).Warn("session memory compaction failed")
			}
		} else {
			// Simple fallback: store first doc as merged, delete rest.
			_, _ = a.store.Store(ctx, group[:1])
			for _, d := range group[1:] {
				_ = a.store.Delete(ctx, d.ID)
			}
		}
	}
}

// SetUserID sets the default user ID for memory scoping. Context values
// (adk.AddSessionValue) take precedence per invocation.
func (a *MemoryAgent) SetUserID(userID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.userID = userID
}

// SetSessionID sets the default session ID. Context values take precedence.
func (a *MemoryAgent) SetSessionID(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionID = sessionID
}
