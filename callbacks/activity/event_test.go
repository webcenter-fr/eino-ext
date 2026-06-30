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

package activity

import (
	"encoding/json"
	"testing"
)

func TestMarshalSSEDataMergesAgent(t *testing.T) {
	b, err := MarshalSSEData(Event{Agent: "supervisor", Type: TypeTextDelta, Data: TextDelta{Delta: "hi"}})
	if err != nil {
		t.Fatalf("MarshalSSEData: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, b)
	}
	if obj["agent"] != "supervisor" {
		t.Fatalf("missing agent: %s", b)
	}
	if obj["delta"] != "hi" {
		t.Fatalf("payload field not preserved: %s", b)
	}
}

func TestMarshalSSEDataNoAgentUnchanged(t *testing.T) {
	b, err := MarshalSSEData(Event{Type: TypeTextDelta, Data: TextDelta{Delta: "hi"}})
	if err != nil {
		t.Fatalf("MarshalSSEData: %v", err)
	}
	if string(b) != `{"delta":"hi"}` {
		t.Fatalf("unexpected body for empty agent: %s", b)
	}
}

func TestMarshalSSEDataNilData(t *testing.T) {
	// nil data, no agent: preserves current null body.
	b, err := MarshalSSEData(Event{Type: TypeTextStarted})
	if err != nil {
		t.Fatalf("MarshalSSEData: %v", err)
	}
	if string(b) != "null" {
		t.Fatalf("want null for nil data without agent, got %s", b)
	}

	// nil data, with agent: emits an object carrying only the agent.
	b, err = MarshalSSEData(Event{Agent: "researcher", Type: TypeTextStarted})
	if err != nil {
		t.Fatalf("MarshalSSEData: %v", err)
	}
	if string(b) != `{"agent":"researcher"}` {
		t.Fatalf("want agent-only object, got %s", b)
	}
}

func TestMarshalSSEDataNonObjectPayloadPreserved(t *testing.T) {
	// A non-object payload (string) cannot host an agent key; preserve it.
	b, err := MarshalSSEData(Event{Agent: "x", Data: "plain"})
	if err != nil {
		t.Fatalf("MarshalSSEData: %v", err)
	}
	if string(b) != `"plain"` {
		t.Fatalf("non-object payload should be unchanged, got %s", b)
	}
}
