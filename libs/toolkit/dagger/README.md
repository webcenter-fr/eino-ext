// Package dagger provides a wrapper around the Dagger engine client for
// building and managing OCI containers with shared cache volumes and egress
// proxy bindings. It is a reusable, non-component library (mirrors
// libs/toolkit/osclient).
//
// Usage:
//
//	cfg := &dagger.EngineConfig{}
//	client, err := dagger.NewClient(ctx, cfg)
//	defer client.Close()
//
//	cont, err := client.Container(ctx, "golang:1.22",
//	    dagger.WithWorkdirMount("/path/to/project"),
//	    dagger.WithCachedVolume("/go/pkg/mod", dagger.CacheKeyForProfile("golang:1.22", "golang")),
//	)
package dagger
