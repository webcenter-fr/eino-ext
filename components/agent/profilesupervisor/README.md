// Package profilesupervisor provides a profile supervisor agent factory that
// dynamically selects, at runtime, the sub-agent whose shell sandbox uses the
// right OCI base image for the task.
//
// The supervisor builds one sub-agent per detected profile (golang, node, python,
// etc.), each backed by a shell tool configured with the matching base image.
//
// Usage:
//
//	cfg := &profilesupervisor.SupervisorConfig{
//	    Model:   myModel,
//	    Workdir: "/path/to/project",
//	}
//	supervisor, err := profilesupervisor.NewProfileSupervisor(ctx, cfg)
//
//	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: supervisor})
//	iter := runner.Run(ctx, []*schema.Message{schema.UserMessage("build the Go project")})
package profilesupervisor
