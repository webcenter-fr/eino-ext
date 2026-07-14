// Package profile provides project-type detection and base-image selection for
// Dagger-backed shell sandboxes. It scans a workdir for marker files and maps
// the detected project type to an OCI base image.
//
// Usage:
//
//	r := profile.NewResolver()
//	profiles, err := r.Resolve(ctx, "/path/to/project")
//	for _, p := range profiles {
//	    fmt.Printf("%s -> %s\n", p.Name, p.BaseImage)
//	}
//
// Polyglot repos (e.g., golang + vue frontend) yield multiple profiles.
// Unknown projects default to alpine:3.20.
package profile
