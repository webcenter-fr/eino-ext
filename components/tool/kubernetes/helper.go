package kubernetes

import _ "embed"

// listOutputGuidance is a shared guidance block appended to the description of all
// list tools (generic and resource list). It instructs the model to narrow queries
// to avoid large responses that blow up the context window.
//
//go:embed prompts/list_output_guidance.md
var listOutputGuidance string

// describeOutputGuidance is a shared guidance block appended to the description of all
// describe tools. It instructs the model to narrow the output to avoid large responses.
//
//go:embed prompts/describe_output_guidance.md
var describeOutputGuidance string
