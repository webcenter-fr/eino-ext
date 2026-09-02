// Package github provides eino tools that interact with GitHub repositories,
// issues, pull requests, releases, branches, and webhooks. It wraps the
// go-github REST client for API operations and go-git for local clone, pull,
// and branch operations.
//
// # Usage
//
//	configs := github.Configs{
//	    "default": github.Config{
//	        Token:    os.Getenv("GITHUB_TOKEN"),
//	        CloneDir: "/tmp/github",
//	    },
//	}
//	tools, err := github.NewAllTools(ctx, configs)
package github
