package argocd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"emperror.dev/errors"
	"github.com/disaster37/goargocdclient/api"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/checkup"
)

const argocdCheckTimeout = 10 * time.Second

// Check probes connectivity and RBAC permissions for all configured ArgoCD
// instances. For each instance it tests every read‑only tool: list endpoints
// are called directly; describe endpoints are tested by listing first, then
// describing the first item. Write tools (create/delete/sync) are not probed.
func Check(ctx context.Context, configs Configs) checkup.Results {
	if len(configs) == 0 {
		return checkup.Results{{
			Component: "argocd",
			Status:    checkup.StatusError,
			Error:     "no ArgoCD instances configured",
		}}
	}

	instances := configs.GetInstanceNames()
	var all checkup.Results

	for _, instance := range instances {
		cfg := configs.GetConfig(instance)
		baseCtx, baseCancel := context.WithTimeout(ctx, argocdCheckTimeout)

		client, err := NewClient(baseCtx, cfg)
		if err != nil {
			all = append(all, clientErrorResults(instance, err)...)
			baseCancel()
			continue
		}

		// Build a single raw HTTP client for name extraction, reusing the
		// same TLS settings derived from the Config convenience fields.
		rawHTTP := newArgoCDHTTPClient(cfg)

		func() {
			defer baseCancel()
			all = append(all, probeInstance(baseCtx, client, rawHTTP, instance, cfg)...)
		}()
	}

	return all
}

func clientErrorResults(instance string, err error) checkup.Results {
	errStr := err.Error()
	return checkup.Results{
		{Component: "argocd_instance_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_application_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_application_describe", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_cluster_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_cluster_describe", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_project_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_project_describe", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_repository_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_repository_describe", Instance: instance, Status: checkup.StatusError, Error: errStr},
		{Component: "argocd_certificate_list", Instance: instance, Status: checkup.StatusError, Error: errStr},
	}
}

// ─── Local types mirroring true ArgoCD REST JSON shape ──────────────────
//
// goargocdclient's ObjectMeta tags Name as json:"name,omitempty" (flat),
// but the ArgoCD REST API nests resource names under "metadata". These local
// types unmarshal the actual wire format so names can be extracted reliably.
// They exist in check.go because they are only needed for health probes;
// normal tool operations use the goargocdclient types directly.

type metadataName struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type appListItem struct {
	Metadata metadataName `json:"metadata"`
}

type appList struct {
	Items []appListItem `json:"items"`
}

type clusterListItem struct {
	Metadata metadataName `json:"metadata"`
	Name     string       `json:"name"`   // display name; some ArgoCD versions include it at top‑level
	Server   string       `json:"server"` // required for Get endpoint
}

type clusterList struct {
	Items []clusterListItem `json:"items"`
}

type projectListItem struct {
	Metadata metadataName `json:"metadata"`
}

type projectList struct {
	Items []projectListItem `json:"items"`
}

// ─── Raw‑HTTP helpers for name extraction ───────────────────────────────

// newArgoCDHTTPClient creates an http.Client from Config convenience fields.
func newArgoCDHTTPClient(cfg Config) *http.Client {
	transport := &http.Transport{}
	if cfg.TLSSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	return &http.Client{Transport: transport, Timeout: argocdCheckTimeout}
}

// doArgoCDListGET makes a GET request to the ArgoCD list endpoint using the
// provided http.Client and returns the raw response body. The path must be
// absolute (e.g. "/api/v1/applications").
func doArgoCDListGET(ctx context.Context, httpClient *http.Client, cfg Config, path string) ([]byte, error) {
	baseURL := strings.TrimRight(cfg.URL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create request")
	}
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "request failed")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read response body")
	}
	if resp.StatusCode >= 400 {
		return nil, errors.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// fetchFirstApp returns the name and namespace of the first application.
func fetchFirstApp(ctx context.Context, httpClient *http.Client, cfg Config) (name, namespace string, _ error) {
	body, err := doArgoCDListGET(ctx, httpClient, cfg, "/api/v1/applications")
	if err != nil {
		return "", "", err
	}
	var list appList
	if err := json.Unmarshal(body, &list); err != nil {
		return "", "", errors.Wrap(err, "failed to unmarshal application list")
	}
	if len(list.Items) == 0 {
		return "", "", errors.New("no applications found")
	}
	return list.Items[0].Metadata.Name, list.Items[0].Metadata.Namespace, nil
}

// fetchClusterServers returns all cluster server URLs and display names
// from the list endpoint. ArgoCD RBAC may grant get on some clusters but
// not others (e.g. in‑cluster vs external), so the checker tries each one.
func fetchClusterServers(ctx context.Context, httpClient *http.Client, cfg Config) (servers, names []string, _ error) {
	body, err := doArgoCDListGET(ctx, httpClient, cfg, "/api/v1/clusters")
	if err != nil {
		return nil, nil, err
	}
	var list clusterList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, nil, errors.Wrap(err, "failed to unmarshal cluster list")
	}
	for _, item := range list.Items {
		name := item.Name
		if name == "" {
			name = item.Metadata.Name
		}
		servers = append(servers, item.Server)
		names = append(names, name)
	}
	if len(servers) == 0 {
		return nil, nil, errors.New("no clusters found")
	}
	return servers, names, nil
}

// fetchFirstProject returns the name of the first project.
func fetchFirstProject(ctx context.Context, httpClient *http.Client, cfg Config) (string, error) {
	body, err := doArgoCDListGET(ctx, httpClient, cfg, "/api/v1/projects")
	if err != nil {
		return "", err
	}
	var list projectList
	if err := json.Unmarshal(body, &list); err != nil {
		return "", errors.Wrap(err, "failed to unmarshal project list")
	}
	if len(list.Items) == 0 {
		return "", errors.New("no projects found")
	}
	return list.Items[0].Metadata.Name, nil
}

// ─── Probe helpers ──────────────────────────────────────────────────────

func probeInstance(ctx context.Context, client api.API, httpClient *http.Client, instance string, cfg Config) checkup.Results {
	var results checkup.Results

	results = append(results, probeInstanceList(ctx, instance))

	listResult, apps, err := probeApplicationList(ctx, client, instance)
	results = append(results, listResult)
	if err == nil && len(apps) > 0 {
		name, namespace, ferr := fetchFirstApp(ctx, httpClient, cfg)
		if ferr != nil {
			results = append(results, checkup.Result{
				Component: "argocd_application_describe",
				Instance:  instance,
				Status:    checkup.StatusError,
				Error:     errors.Wrap(ferr, "failed to extract application name").Error(),
			})
		} else {
			results = append(results, probeApplicationDescribe(ctx, client, instance, name, namespace))
		}
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "argocd_application_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no applications to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "argocd_application_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	cr, clusters, err := probeClusterList(ctx, client, instance)
	results = append(results, cr)
	if err == nil && len(clusters) > 0 {
		servers, names, ferr := fetchClusterServers(ctx, httpClient, cfg)
		if ferr != nil {
			results = append(results, checkup.Result{
				Component: "argocd_cluster_describe",
				Instance:  instance,
				Status:    checkup.StatusError,
				Error:     errors.Wrap(ferr, "failed to extract cluster servers").Error(),
			})
		} else {
			var ok bool
			var lastErr error
			for i := range servers {
				_, cerr := client.Cluster().Get(names[i], &api.ClusterQueryOptions{IdType: "name"})
				if cerr == nil {
					ok = true
					results = append(results, checkup.Result{
						Component: "argocd_cluster_describe",
						Instance:  instance,
						Status:    checkup.StatusOK,
						Message:   fmt.Sprintf("described cluster %q, RBAC ok", names[i]),
					})
					break
				}
				lastErr = cerr
			}
			if !ok {
				results = append(results, checkup.Result{
					Component: "argocd_cluster_describe",
					Instance:  instance,
					Status:    checkup.StatusError,
					Error:     errors.Wrap(lastErr, "failed to describe any cluster").Error(),
				})
			}
		}
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "argocd_cluster_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no clusters to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "argocd_cluster_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	pr, projects, err := probeProjectList(ctx, client, instance)
	results = append(results, pr)
	if err == nil && len(projects) > 0 {
		name, ferr := fetchFirstProject(ctx, httpClient, cfg)
		if ferr != nil {
			results = append(results, checkup.Result{
				Component: "argocd_project_describe",
				Instance:  instance,
				Status:    checkup.StatusError,
				Error:     errors.Wrap(ferr, "failed to extract project name").Error(),
			})
		} else {
			results = append(results, probeProjectDescribe(ctx, client, instance, name))
		}
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "argocd_project_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no projects to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "argocd_project_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	rr, repos, err := probeRepositoryList(ctx, client, instance)
	results = append(results, rr)
	if err == nil && len(repos) > 0 {
		// RepositoryModel.Repo maps directly to the JSON "repo" field;
		// no name‑extraction workaround is needed for repositories.
		results = append(results, probeRepositoryDescribe(ctx, client, instance, repos[0].Repo))
	} else if err == nil {
		results = append(results, checkup.Result{
			Component: "argocd_repository_describe",
			Instance:  instance,
			Status:    checkup.StatusLimited,
			Message:   "no repositories to test describe",
		})
	} else {
		results = append(results, checkup.Result{
			Component: "argocd_repository_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     "dependency failed",
		})
	}

	cr2, _, _ := probeCertificateList(ctx, client, instance)
	results = append(results, cr2)

	return results
}

func probeInstanceList(ctx context.Context, instance string) checkup.Result {
	return checkup.Result{
		Component: "argocd_instance_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
	}
}

func probeApplicationList(ctx context.Context, client api.API, instance string) (checkup.Result, []*api.ApplicationModel, error) {
	resp, err := client.Application().List(&api.ApplicationListOptions{})
	if err != nil {
		return checkup.Result{
			Component: "argocd_application_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list applications").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d applications found, RBAC ok", len(resp.Items))
	return checkup.Result{
		Component: "argocd_application_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, resp.Items, nil
}

func probeApplicationDescribe(ctx context.Context, client api.API, instance, name, namespace string) checkup.Result {
	_, err := client.Application().Get(name, &api.ApplicationGetOptions{
		AppNamespace: namespace,
	})
	if err != nil {
		return checkup.Result{
			Component: "argocd_application_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to describe application").Error(),
		}
	}
	return checkup.Result{
		Component: "argocd_application_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described application %q, RBAC ok", name),
	}
}

func probeClusterList(ctx context.Context, client api.API, instance string) (checkup.Result, []*api.ClusterModel, error) {
	resp, err := client.Cluster().List(&api.ClusterQueryOptions{})
	if err != nil {
		return checkup.Result{
			Component: "argocd_cluster_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list clusters").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d clusters found, RBAC ok", len(resp.Items))
	return checkup.Result{
		Component: "argocd_cluster_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, resp.Items, nil
}

func probeProjectList(ctx context.Context, client api.API, instance string) (checkup.Result, []*api.ProjectModel, error) {
	resp, err := client.Project().List()
	if err != nil {
		return checkup.Result{
			Component: "argocd_project_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list projects").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d projects found, RBAC ok", len(resp.Items))
	return checkup.Result{
		Component: "argocd_project_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, resp.Items, nil
}

func probeProjectDescribe(ctx context.Context, client api.API, instance, name string) checkup.Result {
	_, err := client.Project().Get(name)
	if err != nil {
		return checkup.Result{
			Component: "argocd_project_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to describe project").Error(),
		}
	}
	return checkup.Result{
		Component: "argocd_project_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described project %q, RBAC ok", name),
	}
}

func probeRepositoryList(ctx context.Context, client api.API, instance string) (checkup.Result, []*api.RepositoryModel, error) {
	resp, err := client.Repository().List(&api.RepositoryQueryOptions{})
	if err != nil {
		return checkup.Result{
			Component: "argocd_repository_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list repositories").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d repositories found, RBAC ok", len(resp.Items))
	return checkup.Result{
		Component: "argocd_repository_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, resp.Items, nil
}

func probeRepositoryDescribe(ctx context.Context, client api.API, instance, repo string) checkup.Result {
	_, err := client.Repository().Get(repo, &api.RepositoryQueryOptions{})
	if err != nil {
		return checkup.Result{
			Component: "argocd_repository_describe",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to describe repository").Error(),
		}
	}
	return checkup.Result{
		Component: "argocd_repository_describe",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   fmt.Sprintf("described repository %q, RBAC ok", repo),
	}
}

func probeCertificateList(ctx context.Context, client api.API, instance string) (checkup.Result, []api.CertificateModel, error) {
	resp, err := client.Certificate().List(&api.CertificateQuery{})
	if err != nil {
		return checkup.Result{
			Component: "argocd_certificate_list",
			Instance:  instance,
			Status:    checkup.StatusError,
			Error:     errors.Wrap(err, "failed to list certificates").Error(),
		}, nil, err
	}
	msg := fmt.Sprintf("%d certificates found, RBAC ok", len(resp.Items))
	return checkup.Result{
		Component: "argocd_certificate_list",
		Instance:  instance,
		Status:    checkup.StatusOK,
		Message:   msg,
	}, resp.Items, nil
}
