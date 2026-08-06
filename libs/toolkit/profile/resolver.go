package profile

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"emperror.dev/errors"
)

// Resolve scans the workdir for project markers and returns matching profiles.
func (r *Resolver) Resolve(ctx context.Context, workdir string) ([]Profile, error) {
	if workdir == "" {
		return nil, errors.New("workdir is empty")
	}

	seen := make(map[string]bool)
	var profiles []Profile

	for marker, profileName := range markerToProfile {
		markerPath := filepath.Join(workdir, marker)
		if _, err := os.Stat(markerPath); err == nil {
			if !seen[profileName] {
				seen[profileName] = true
				image, ok := r.ImageMap[profileName]
				if !ok {
					image = DefaultImageMap[profileName]
				}
				profiles = append(profiles, buildProfile(profileName, image, marker))
			}
		}
	}

	if len(profiles) == 0 {
		profiles = append(profiles, buildDefaultProfile())
	}

	return profiles, nil
}

func buildProfile(profileName, image, marker string) Profile {
	p := Profile{
		Name:      profileName,
		BaseImage: image,
		SystemPrompt: buildSystemPrompt(profileName, marker),
	}
	switch profileName {
	case "golang":
		p.ToolPresets = []string{"go", "gofmt", "dlv"}
		p.InstallCmd = []string{"apt-get", "update", "&&", "apt-get", "install", "-y"}
	case "node":
		p.ToolPresets = []string{"npm", "npx", "node"}
		p.InstallCmd = []string{"apt-get", "update", "&&", "apt-get", "install", "-y"}
		p.Env = map[string]string{
			"NODE_ENV": "development",
		}
	case "python":
		p.ToolPresets = []string{"python3", "pip", "pip3"}
		p.InstallCmd = []string{"apt-get", "update", "&&", "apt-get", "install", "-y"}
	case "java":
		p.ToolPresets = []string{"java", "javac", "mvn", "gradle"}
		p.InstallCmd = []string{"apt-get", "update", "&&", "apt-get", "install", "-y"}
	case "rust":
		p.ToolPresets = []string{"rustc", "cargo", "rustup"}
		p.InstallCmd = []string{"apt-get", "update", "&&", "apt-get", "install", "-y"}
	case "php":
		p.ToolPresets = []string{"php", "composer"}
		p.InstallCmd = []string{"apt-get", "update", "&&", "apt-get", "install", "-y"}
	}
	return p
}

func buildDefaultProfile() Profile {
	return Profile{
		Name:         "default",
		BaseImage:    "alpine:3.20",
		SystemPrompt: "You are using a shell sandbox backed by Alpine Linux. You can install additional tools with `apk add`.",
		InstallCmd:   []string{"apk", "add"},
	}
}

func buildSystemPrompt(profileName, marker string) string {
	var sb strings.Builder
	sb.WriteString("You are using a shell sandbox backed by a ")
	sb.WriteString(profileName)
	sb.WriteString(" development container (")
	sb.WriteString(marker)
	sb.WriteString(" detected).\n\n")
	sb.WriteString("The container has the ")
	sb.WriteString(profileName)
	sb.WriteString(" toolchain pre-installed. You can run ")
	switch profileName {
	case "golang":
		sb.WriteString("`go build`, `go test`, `go mod tidy`, and other Go commands directly.")
	case "node":
		sb.WriteString("`npm install`, `npm test`, `npm run build`, and other Node.js commands directly.")
	case "python":
		sb.WriteString("`pip install`, `python`, `pytest`, and other Python commands directly.")
	case "java":
		sb.WriteString("`mvn test`, `mvn package`, `gradle build`, and other Java commands directly.")
	case "rust":
		sb.WriteString("`cargo build`, `cargo test`, `rustc`, and other Rust commands directly.")
	case "php":
		sb.WriteString("`composer install`, `php vendor/bin/phpunit`, and other PHP commands directly.")
	}
	sb.WriteString(" You can also install additional system packages as root with apt-get.")
	return sb.String()
}
