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

import "context"

// sessionKeyType is an unexported context key type to avoid collisions.
type sessionKeyType struct{}

var sessionKey = sessionKeyType{}

// WithSession returns a copy of ctx carrying the activity session id. Callers set
// it at run start so the Handler can correlate concurrent graph runs to distinct
// fan-out buckets without cross-talk.
func WithSession(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sessionKey, id)
}

// SessionFromContext reads the session id set by WithSession. The boolean is
// false when no session was set; an empty string is still a valid bucket the
// Handler publishes to.
func SessionFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(sessionKey).(string)
	return id, ok
}
