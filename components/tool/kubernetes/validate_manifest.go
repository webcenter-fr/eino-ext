package kubernetes

import (
	"fmt"

	"emperror.dev/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// validateManifestSecurity checks a Kubernetes manifest for dangerous pod specs.
// It rejects resources containing privileged containers, host namespace access,
// hostPath volumes, and other high-risk configurations.
//
// For Pod resources, it inspects spec directly.
// For Job resources, it inspects spec.template.spec.
// For CronJob resources, it inspects spec.jobTemplate.spec.template.spec.
func validateManifestSecurity(obj *unstructured.Unstructured) error {
	kind := obj.GetKind()
	podSpec, err := extractPodSpec(obj, kind)
	if err != nil {
		return err
	}
	if podSpec == nil {
		return nil
	}
	return validatePodSpec(podSpec)
}

func extractPodSpec(obj *unstructured.Unstructured, kind string) (map[string]any, error) {
	switch kind {
	case "Pod":
		return nestedMap(obj.Object, "spec")
	case "Job":
		return nestedMap(obj.Object, "spec", "template", "spec")
	case "CronJob":
		return nestedMap(obj.Object, "spec", "jobTemplate", "spec", "template", "spec")
	default:
		return nil, nil
	}
}

func nestedMap(m map[string]any, fields ...string) (map[string]any, error) {
	for _, field := range fields {
		v, ok := m[field]
		if !ok {
			return nil, fmt.Errorf("missing field %q", field)
		}
		m, ok = v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("field %q is not a map", field)
		}
	}
	return m, nil
}

// typedSlice safely extracts a []any from a map, returning nil on missing or
// wrong-typed keys (avoids panics from direct type assertions on nil).
func typedSlice(m map[string]any, key string) []any {
	s, _ := m[key].([]any)
	return s
}

var (
	errHostNetwork    = errors.New("hostNetwork is not allowed")
	errHostPID        = errors.New("hostPID is not allowed")
	errHostIPC        = errors.New("hostIPC is not allowed")
	errPrivileged     = errors.New("privileged containers are not allowed")
	errSYSADMIN       = errors.New("SYS_ADMIN capability is not allowed")
	errHostPathVolume = errors.New("hostPath volumes are not allowed")
	errDangerousMount = errors.New("mounting sensitive host path is not allowed")
)

var sensitiveHostPaths = map[string]bool{
	"/proc":               true,
	"/sys":                true,
	"/etc/kubernetes":     true,
	"/var/run/docker.sock": true,
	"/var/run/crio":       true,
	"/var/run/containerd": true,
}

func validatePodSpec(spec map[string]any) error {
	if b, ok := spec["hostNetwork"].(bool); ok && b {
		return errHostNetwork
	}
	if b, ok := spec["hostPID"].(bool); ok && b {
		return errHostPID
	}
	if b, ok := spec["hostIPC"].(bool); ok && b {
		return errHostIPC
	}

	// Check containers for security and mount violations in one pass.
	containers, _ := spec["containers"].([]any)
	for _, cRaw := range containers {
		c, ok := cRaw.(map[string]any)
		if !ok {
			continue
		}
		if err := validateContainerSecurity(c); err != nil {
			return err
		}
		for _, vmRaw := range typedSlice(c, "volumeMounts") {
			vm, ok := vmRaw.(map[string]any)
			if !ok {
				continue
			}
			mountPath, _ := vm["mountPath"].(string)
			if sensitiveHostPaths[mountPath] {
				return errDangerousMount
			}
		}
	}

	// Check hostPath volumes.
	for _, vRaw := range typedSlice(spec, "volumes") {
		v, ok := vRaw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasHostPath := v["hostPath"]; hasHostPath {
			return errHostPathVolume
		}
	}

	return nil
}

func validateContainerSecurity(container map[string]any) error {
	sc, ok := container["securityContext"].(map[string]any)
	if !ok {
		return nil
	}

	if b, ok := sc["privileged"].(bool); ok && b {
		return errPrivileged
	}

	caps, ok := sc["capabilities"].(map[string]any)
	if !ok {
		return nil
	}
	add, ok := caps["add"].([]any)
	if !ok {
		return nil
	}
	for _, capRaw := range add {
		capStr, ok := capRaw.(string)
		if ok && capStr == "SYS_ADMIN" {
			return errSYSADMIN
		}
	}

	return nil
}
