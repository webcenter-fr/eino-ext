package argocd

type ApplicationListResponse struct {
	Items []ApplicationSummary `json:"items"`
}

type ApplicationSummary struct {
	Metadata ObjectMeta          `json:"metadata"`
	Spec     map[string]any      `json:"spec,omitempty"`
	Status   *ApplicationStatus  `json:"status,omitempty"`
}

type ApplicationResponse struct {
	Metadata ObjectMeta          `json:"metadata"`
	Spec     map[string]any      `json:"spec"`
	Status   *ApplicationStatus  `json:"status,omitempty"`
}

type ObjectMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
}

type ApplicationStatus struct {
	Health    *HealthStatus    `json:"health,omitempty"`
	Sync      *SyncStatus      `json:"sync,omitempty"`
	Summary   *AppSummary      `json:"summary,omitempty"`
	Resources []ResourceStatus `json:"resources,omitempty"`
}

type HealthStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type SyncStatus struct {
	Status   string `json:"status"`
	Revision string `json:"revision,omitempty"`
}

type AppSummary struct {
	Images []string `json:"images,omitempty"`
}

type ResourceStatus struct {
	Name      string `json:"name,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Status    string `json:"status,omitempty"`
	Health    string `json:"health,omitempty"`
}

type SyncRequest struct {
	Revision string `json:"revision,omitempty"`
	Prune    bool   `json:"prune,omitempty"`
	DryRun   bool   `json:"dryRun,omitempty"`
}

type ApplicationCreateRequest struct {
	Metadata ObjectMeta       `json:"metadata"`
	Spec     ApplicationSpec  `json:"spec"`
}

type ApplicationSpec struct {
	Source      *ApplicationSource      `json:"source"`
	Destination *ApplicationDestination `json:"destination"`
	Project     string                  `json:"project,omitempty"`
}

type ApplicationSource struct {
	RepoURL        string `json:"repoURL"`
	Path           string `json:"path,omitempty"`
	TargetRevision string `json:"targetRevision,omitempty"`
}

type ApplicationDestination struct {
	Server    string `json:"server,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
}

type ProjectListResponse struct {
	Items []ProjectSummary `json:"items"`
}

type ProjectSummary struct {
	Metadata ObjectMeta     `json:"metadata"`
	Spec     map[string]any `json:"spec,omitempty"`
	Status   map[string]any `json:"status,omitempty"`
}

type ProjectResponse struct {
	Metadata ObjectMeta     `json:"metadata"`
	Spec     map[string]any `json:"spec"`
	Status   map[string]any `json:"status,omitempty"`
}

type APIError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
