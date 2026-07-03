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

// Package safety provides shared primitives for building a safety control layer
// around LLM tool invocations. It covers audit trails, CEL-based policy
// evaluation, gate logic (dry-run/confirmed), and controller ownership detection
// for Kubernetes resources.
//
// These primitives are not tied to a specific eino abstraction. The ADK
// middleware implementation lives in components/middleware/safety/.
package safety

// OperationType classifies the kind of tool operation being performed.
type OperationType string

const (
	OpCreate OperationType = "create"
	OpUpdate OperationType = "update"
	OpDelete OperationType = "delete"
	OpSync   OperationType = "sync"
	OpExec   OperationType = "exec"
)

// Phase captures the lifecycle phase of a tool invocation.
type Phase string

const (
	PhaseRead     Phase = "read"      // Read-only tool call (list, describe, log)
	PhaseDryRun   Phase = "dry-run"   // Write tool dry-run simulation
	PhaseExecute  Phase = "execute"   // Write tool actual execution
	PhaseRejected Phase = "rejected"  // Denied by gate or policy
)

// MutabilityLevel marks whether a tool is read-only or write/mutative.
type MutabilityLevel string

const (
	MutabilityReadOnly MutabilityLevel = "readonly"
	MutabilityWrite    MutabilityLevel = "write"
)
