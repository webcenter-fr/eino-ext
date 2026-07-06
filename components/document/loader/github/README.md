# GitHub document loader

`github` wraps the URL document loader with optional Bearer token
authentication for loading documents from private GitHub repositories.

## Usage

```go
import "github.com/webcenter-fr/eino-ext/components/document/loader/github"

loader, err := github.NewGithubLoader(ctx, "ghp_xxx")
docs, err := loader.Load(ctx, document.Source{
    URI: "https://raw.githubusercontent.com/owner/repo/main/docs/README.md",
})
```

When the token is empty, the loader behaves as a plain, unauthenticated URL
loader.
