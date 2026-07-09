package kubernetes

import (
	"fmt"
	"strings"

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
	errHostNetwork             = errors.New("hostNetwork is not allowed")
	errHostPID                 = errors.New("hostPID is not allowed")
	errHostIPC                 = errors.New("hostIPC is not allowed")
	errPrivileged              = errors.New("privileged containers are not allowed")
	errSYSADMIN                = errors.New("SYS_ADMIN capability is not allowed")
	errPrivilegeEscalation     = errors.New("allowPrivilegeEscalation must be false; use true only when explicitly approved")
	errHostPathVolume          = errors.New("hostPath volumes are not allowed")
	errDangerousMount          = errors.New("mounting sensitive host path is not allowed")
	errDangerousSubPath        = errors.New("volume mount subPath traversal is not allowed")
	errDangerousCapabilityFmt  = "capability %q is not allowed"
)

// dangerousCapabilities lists Linux capabilities that grant a container
// privileged access to the host or bypass namespace isolation.
var dangerousCapabilities = map[string]bool{
	"SYS_ADMIN":    true,
	"SYS_PTRACE":   true,
	"SYS_MODULE":   true,
	"SYS_RAWIO":    true,
	"SYS_BOOT":     true,
	"NET_ADMIN":    true,
	"NET_RAW":      true,
	"MAC_ADMIN":    true,
	"MAC_OVERRIDE": true,
	"DAC_OVERRIDE": true,
	"BPF":          true,
	"PERFMON":      true,
}

var sensitiveHostPaths = map[string]bool{
	"/proc":                 true,
	"/sys":                  true,
	"/etc/kubernetes":       true,
	"/var/run/docker.sock":  true,
	"/var/run/crio":         true,
	"/var/run/containerd":   true,
}

// validateContainerPaths checks all container lists (containers, initContainers,
// ephemeralContainers) in a pod spec for security violations.
func validateContainerPaths(spec map[string]any) error {
	containerLists := []string{"containers", "initContainers", "ephemeralContainers"}
	for _, listKey := range containerLists {
		for _, cRaw := range typedSlice(spec, listKey) {
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
				// Check mountPath for sensitive directories.
				mountPath, _ := vm["mountPath"].(string)
				if sensitiveHostPaths[mountPath] {
					return errDangerousMount
				}
				// Check subPath for directory traversal.
				subPath, _ := vm["subPath"].(string)
				if subPath != "" && (strings.Contains(subPath, "..") || strings.HasPrefix(subPath, "/")) {
					return errDangerousSubPath
				}
				// Check subPathExpr similarly.
				subPathExpr, _ := vm["subPathExpr"].(string)
				if subPathExpr != "" && strings.Contains(subPathExpr, "..") {
					return errDangerousSubPath
				}
			}
		}
	}
	return nil
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

	// Check all container types (containers, initContainers, ephemeralContainers).
	if err := validateContainerPaths(spec); err != nil {
		return err
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

	// Check allowPrivilegeEscalation: must be explicitly false when set.
	if v, ok := sc["allowPrivilegeEscalation"]; ok {
		if b, ok := v.(bool); !ok || b {
			return errPrivilegeEscalation
		}
	}

	// Check dangerous capabilities.
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
		if !ok {
			continue
		}
		if capStr == "SYS_ADMIN" {
			return errSYSADMIN
		}
		if dangerousCapabilities[capStr] {
			return errors.Errorf(errDangerousCapabilityFmt, capStr)
		}
	}

	return nil
}
