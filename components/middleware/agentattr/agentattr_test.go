/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package agentattr

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

func TestNewRequiresAgentName(t *testing.T) {
	if _, err := New(&Config{}); err == nil {
		t.Fatal("expected error for empty AgentName")
	}
	if _, err := New(nil); err == nil {
		t.Fatal("expected error for nil config")
	}
	if _, err := New(&Config{AgentName: "supervisor"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMiddlewareInjectsAgentOnContext(t *testing.T) {
	m, err := New(&Config{AgentName: "supervisor"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	assertAgent := func(ctx context.Context, where string) {
		if name, ok := activity.AgentFromContext(ctx); !ok || name != "supervisor" {
			t.Fatalf("%s: agent=%q ok=%v, want supervisor", where, name, ok)
		}
	}

	// BeforeAgent.
	ctx, _, err := m.BeforeAgent(context.Background(), &adk.ChatModelAgentContext{})
	if err != nil {
		t.Fatalf("BeforeAgent: %v", err)
	}
	assertAgent(ctx, "BeforeAgent")

	// BeforeModelRewriteState.
	ctx, _, err = m.BeforeModelRewriteState(context.Background(), &adk.ChatModelAgentState{}, &adk.ModelContext{})
	if err != nil {
		t.Fatalf("BeforeModelRewriteState: %v", err)
	}
	assertAgent(ctx, "BeforeModelRewriteState")

	// WrapInvokableToolCall threads the agent onto the endpoint's context.
	endpoint := func(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
		assertAgent(ctx, "WrapInvokableToolCall endpoint")
		return "ok", nil
	}
	wrapped, err := m.WrapInvokableToolCall(context.Background(), endpoint, &adk.ToolContext{})
	if err != nil {
		t.Fatalf("WrapInvokableToolCall: %v", err)
	}
	if _, err := wrapped(context.Background(), "{}"); err != nil {
		t.Fatalf("wrapped endpoint: %v", err)
	}
}
