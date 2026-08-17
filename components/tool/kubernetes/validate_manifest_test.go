package kubernetes

import (
	"testing"

	"emperror.dev/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestValidateManifest_LegitimatePod(t *testing.T) {
	tests := []struct {
		name     string
		manifest map[string]any
	}{
		{
			name: "simple pod",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "test-pod"},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "nginx",
							"image": "nginx:latest",
						},
					},
				},
			},
		},
		{
			name: "deployment (no pod-level checks)",
			manifest: map[string]any{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata":   map[string]any{"name": "test-deploy"},
				"spec":       map[string]any{},
			},
		},
		{
			name: "service (no pod-level checks)",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata":   map[string]any{"name": "test-svc"},
			},
		},
		{
			name: "pod with non-privileged security context",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "test-pod"},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "app",
							"image": "app:latest",
							"securityContext": map[string]any{
								"runAsNonRoot": true,
								"capabilities": map[string]any{
									"drop": []any{"ALL"},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "legitimate job",
			manifest: map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "Job",
				"metadata":   map[string]any{"name": "test-job"},
				"spec": map[string]any{
					"template": map[string]any{
						"spec": map[string]any{
							"containers": []any{
								map[string]any{
									"name":  "job-container",
									"image": "busybox",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "legitimate cronjob",
			manifest: map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "CronJob",
				"metadata":   map[string]any{"name": "test-cron"},
				"spec": map[string]any{
					"schedule": "*/5 * * * *",
					"jobTemplate": map[string]any{
						"spec": map[string]any{
							"template": map[string]any{
								"spec": map[string]any{
									"containers": []any{
										map[string]any{
											"name":  "cron-container",
											"image": "busybox",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{Object: tt.manifest}
			if err := validateManifestSecurity(obj); err != nil {
				t.Errorf("expected no error for legitimate %s, got: %v", tt.name, err)
			}
		})
	}
}

func TestValidateManifest_PrivilegedPod(t *testing.T) {
	tests := []struct {
		name     string
		manifest map[string]any
		wantErr  error
	}{
		{
			name: "hostNetwork pod",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "bad-pod"},
				"spec": map[string]any{
					"hostNetwork": true,
					"containers": []any{
						map[string]any{"name": "app", "image": "app:latest"},
					},
				},
			},
			wantErr: errHostNetwork,
		},
		{
			name: "hostPID pod",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "bad-pod"},
				"spec": map[string]any{
					"hostPID": true,
					"containers": []any{
						map[string]any{"name": "app", "image": "app:latest"},
					},
				},
			},
			wantErr: errHostPID,
		},
		{
			name: "hostIPC pod",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "bad-pod"},
				"spec": map[string]any{
					"hostIPC": true,
					"containers": []any{
						map[string]any{"name": "app", "image": "app:latest"},
					},
				},
			},
			wantErr: errHostIPC,
		},
		{
			name: "privileged container",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "bad-pod"},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "app",
							"image": "app:latest",
							"securityContext": map[string]any{
								"privileged": true,
							},
						},
					},
				},
			},
			wantErr: errPrivileged,
		},
		{
			name: "SYS_ADMIN capability",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "bad-pod"},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "app",
							"image": "app:latest",
							"securityContext": map[string]any{
								"capabilities": map[string]any{
									"add": []any{"SYS_ADMIN"},
								},
							},
						},
					},
				},
			},
			wantErr: errSYSADMIN,
		},
		{
			name: "hostPath volume",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "bad-pod"},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "app",
							"image": "app:latest",
							"volumeMounts": []any{
								map[string]any{"name": "host-volume", "mountPath": "/data"},
							},
						},
					},
					"volumes": []any{
						map[string]any{
							"name": "host-volume",
							"hostPath": map[string]any{
								"path": "/tmp",
							},
						},
					},
				},
			},
			wantErr: errHostPathVolume,
		},
		{
			name: "dangerous volume mount /proc",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "bad-pod"},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "app",
							"image": "app:latest",
							"volumeMounts": []any{
								map[string]any{"name": "proc-volume", "mountPath": "/proc"},
							},
						},
					},
					"volumes": []any{
						map[string]any{
							"name":     "proc-volume",
							"emptyDir": map[string]any{},
						},
					},
				},
			},
			wantErr: errDangerousMount,
		},
		{
			name: "dangerous volume mount /var/run/docker.sock",
			manifest: map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata":   map[string]any{"name": "bad-pod"},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "app",
							"image": "app:latest",
							"volumeMounts": []any{
								map[string]any{"name": "dockersock", "mountPath": "/var/run/docker.sock"},
							},
						},
					},
					"volumes": []any{
						map[string]any{
							"name":     "dockersock",
							"emptyDir": map[string]any{},
						},
					},
				},
			},
			wantErr: errDangerousMount,
		},
		{
			name: "job with privileged container",
			manifest: map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "Job",
				"metadata":   map[string]any{"name": "bad-job"},
				"spec": map[string]any{
					"template": map[string]any{
						"spec": map[string]any{
							"containers": []any{
								map[string]any{
									"name":  "bad-container",
									"image": "evil:latest",
									"securityContext": map[string]any{
										"privileged": true,
									},
								},
							},
						},
					},
				},
			},
			wantErr: errPrivileged,
		},
		{
			name: "cronjob with hostNetwork",
			manifest: map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "CronJob",
				"metadata":   map[string]any{"name": "bad-cron"},
				"spec": map[string]any{
					"schedule": "*/5 * * * *",
					"jobTemplate": map[string]any{
						"spec": map[string]any{
							"template": map[string]any{
								"spec": map[string]any{
									"hostNetwork": true,
									"containers": []any{
										map[string]any{"name": "app", "image": "app:latest"},
									},
								},
							},
						},
					},
				},
			},
			wantErr: errHostNetwork,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{Object: tt.manifest}
			err := validateManifestSecurity(obj)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tt.name)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}
