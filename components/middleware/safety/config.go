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

// Package safety provides an adk.ChatModelAgentMiddleware that enforces a safety
// control layer around LLM tool invocations. It combines audit trails, CEL-based
// policy evaluation, gate logic (dry-run/confirmed), and optional ownership
// detection for Kubernetes resources.
//
// The middleware applies to all tools (read + write):
//   - Audit: ALL tool calls are recorded.
//   - Policy: ALL tool calls are evaluated against CEL expressions.
//
// The gate (dry-run/confirmed) applies only to write tools:
//   - Write tools must first be called with dryRun=true to preview the result.
//   - Then they must be re-called with confirmed=true after user approval.
//   - Calling a write tool without dryRun or confirmed is rejected.
package safety

import (
	"github.com/webcenter-fr/eino-ext/libs/toolkit/safety"
)

// Config configures the safety middleware.
type Config struct {
	// WriteToolNames lists tool names that perform write/mutative operations.
	// Read-only tools not in this list skip the gate (dry-run/confirmed) but are
	// still audited and subject to policy evaluation.
	WriteToolNames []string `json:"writeToolNames" jsonschema:"description=Tool names that perform write/mutative operations"`

	// AuditSink receives audit events for EVERY tool call (read + write). Required.
	// If nil, defaults to LogSink (structured logging).
	AuditSink safety.AuditSink `json:"-"`

	// Policy is evaluated before execution for ALL tool calls (read + write). Optional.
	// If nil, all tool calls are allowed (policy pass-through).
	Policy safety.Policy `json:"-"`

	// CheckOwnership enables controller/operator ownership detection for K8s tools.
	// Only applies to write operations on existing resources (update/delete).
	// Note: ownership detection runs in the tool itself, not in middleware.
	// This flag is reserved for future middleware-level ownership checks.
	CheckOwnership bool `json:"checkOwnership" jsonschema:"description=Enable controller/operator ownership detection for K8s resources"`
}
