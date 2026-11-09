package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"poddy/internal/sshca"
	"poddy/internal/sshkeys"
	"poddy/internal/templates"
)

const (
	AppName                = "poddy"
	ContainerName          = "workspace"
	BrowserVSCodeContainer = "code-server"
	WorkspacePath          = "/home/dev"

	labelName      = "app.kubernetes.io/name"
	labelManagedBy = "app.kubernetes.io/managed-by"
	labelWorkspace = "poddy.io/workspace"
	labelTemplate  = "poddy.io/template"
	labelVolume    = "poddy.io/volume"

	// annotationOwner holds the authenticated owner. It is an annotation rather
	// than a label because email addresses (and some usernames) are not valid
	// Kubernetes label values.
	annotationOwner = "poddy.io/owner"
	// annotationExternalAuthEnv records the environment variable names injected
	// from external auth so tokens are never written to the stopped-state ConfigMap.
	annotationExternalAuthEnv = "poddy.io/external-auth-env"

	envPortalURL       = "PODDY_PORTAL_URL"
	envWorkspaceName   = "PODDY_WORKSPACE_NAME"
	envWorkspaceToken  = "PODDY_WORKSPACE_TOKEN"
	envExternalAuthIDs = "PODDY_EXTERNAL_AUTH_IDS"
	envGitAskpass      = "GIT_ASKPASS"
	envGitPrompt       = "GIT_TERMINAL_PROMPT"
	envAgentURL        = "PODDY_AGENT_URL"
	envAgentToken      = "PODDY_AGENT_TOKEN"
)

var (
	// ErrAlreadyRunning is returned when starting a running workspace.
	ErrAlreadyRunning = errors.New("workspace is already running")
	// ErrNotRunning is returned when stopping a stopped workspace.
	ErrNotRunning = errors.New("workspace is not running")
)

type Workspace struct {
	Name          string    `json:"name"`
	Template      string    `json:"template"`
	Phase         string    `json:"phase"`
	Image         string    `json:"image"`
	Owner         string    `json:"owner,omitempty"`
	Persistent    bool      `json:"persistent"`
	BrowserVSCode bool      `json:"browserVSCode,omitempty"`
	CPU           string    `json:"cpu,omitempty"`
	Memory        string    `json:"memory,omitempty"`
	Storage       string    `json:"storage,omitempty"`
	Region        string    `json:"region,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	Schedule      *Schedule `json:"schedule,omitempty"`
	// StoppedBySchedule is internal; it prevents the scheduler from restarting
	// a workspace that a user stopped manually.
	StoppedBySchedule bool `json:"-"`
}

type CreateRequest struct {
	Name       string            `json:"name"`
	Template   string            `json:"template"`
	Parameters map[string]string `json:"parameters"`

	Image   string `json:"image,omitempty"`
	CPU     string `json:"cpu,omitempty"`
	Memory  string `json:"memory,omitempty"`
	Storage string `json:"storage,omitempty"`

	// Owner and PortalURL are set by the portal from the authenticated request.
	Owner     string `json:"-"`
	PortalURL string `json:"-"`

	ExternalAuthReady bool              `json:"externalAuthReady,omitempty"`
	ExternalAuthEnv   map[string]string `json:"externalAuthEnv,omitempty"`
	WorkspaceToken    string            `json:"workspaceToken,omitempty"`
	Schedule          *Schedule         `json:"schedule,omitempty"`

	// QuotaCheck is called with the rendered resource requests before any
	// Kubernetes object is created.
	QuotaCheck func(ResourceUsage) error `json:"-"`
}

// ExternalAuthFunc returns the environment variables injected into a workspace.
// It may return *externalauth.AuthRequiredError when a required provider is not
// connected.
type ExternalAuthFunc func(ctx context.Context, owner, workspace, portalURL string, requirements []templates.ExternalAuth) (map[string]string, error)

// WorkspaceTokenFunc signs the bearer token used by the in-workspace helper.
type WorkspaceTokenFunc func(workspace, owner string) (string, error)

type Manager struct {
	Client              kubernetes.Interface
	Rest                *rest.Config
	Namespace           string
	Templates           *templates.Catalog
	DefaultTemplate     string
	DefaultStorageClass string
	ImageRegistry       string
	ImagePullSecrets    []string
	AuthorizedKeys      []string
	SSHKeys             sshkeys.Keyring
	SSHCA               *sshca.CA
	SSHRequireAgent     bool
	StateConfigMap      string
	InstanceID          string
	PortalURL           string
	Region              string
	HelperImage         string
	ExternalAuth        ExternalAuthFunc
	ExternalAuthToken   WorkspaceTokenFunc
	AgentURL            string
	AgentToken          WorkspaceTokenFunc
}

func PodName(name string) string {
	return "poddy-" + name
}

func labels(name string) map[string]string {
	return map[string]string{
		labelName:      AppName,
		labelManagedBy: AppName,
		labelWorkspace: name,
	}
}

func (m *Manager) List(ctx context.Context) ([]Workspace, error) {
	pods, err := m.Client.CoreV1().Pods(m.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelName + "=" + AppName,
	})
	if err != nil {
		return nil, err
	}

	storage := map[string]string{}
	claims, err := m.Client.CoreV1().PersistentVolumeClaims(m.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelName + "=" + AppName,
	})
	if err != nil {
		return nil, err
	}
	for i := range claims.Items {
		claim := &claims.Items[i]
		name := claim.Labels[labelWorkspace]
		if name == "" {
			name = strings.TrimPrefix(claim.Name, "poddy-")
		}
		if quantity, ok := claim.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			storage[name] = quantity.String()
		}
	}

	out := make([]Workspace, 0, len(pods.Items))
	seen := make(map[string]bool, len(pods.Items))
	for i := range pods.Items {
		ws := toWorkspace(&pods.Items[i])
		if ws.Storage == "" {
			ws.Storage = storage[ws.Name]
		}
		out = append(out, ws)
		seen[ws.Name] = true
	}

	states, err := m.stateStore().Load(ctx)
	if err != nil {
		return nil, err
	}
	for name, state := range states {
		if seen[name] {
			continue
		}
		if state.Name == "" {
			state.Name = name
		}
		ws := workspaceFromState(state, "Stopped")
		if ws.Storage == "" {
			ws.Storage = storage[name]
		}
		out = append(out, ws)
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// State returns the stored spec of a stopped workspace.
func (m *Manager) State(ctx context.Context, name string) (WorkspaceState, error) {
	states, err := m.stateStore().Load(ctx)
	if err != nil {
		return WorkspaceState{}, err
	}
	state, ok := states[name]
	if !ok {
		return WorkspaceState{}, apierrors.NewNotFound(schema.GroupResource{Resource: "workspaces"}, name)
	}
	if state.Name == "" {
		state.Name = name
	}
	return state, nil
}

func (m *Manager) stateStore() *StateStore {
	name := m.StateConfigMap
	if name == "" {
		name = DefaultStateConfigMap
	}
	return NewStateStore(m.Client, m.Namespace, name)
}

// ListByOwner returns the workspaces owned by the identity. The portal filters
// in memory because owner IDs (emails) are annotations, not label values.
func (m *Manager) ListByOwner(ctx context.Context, owner string) ([]Workspace, error) {
	if owner == "" {
		return []Workspace{}, nil
	}
	all, err := m.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Workspace, 0, len(all))
	for _, ws := range all {
		if ws.Owner == owner {
			out = append(out, ws)
		}
	}
	return out, nil
}

// Owner returns the workspace owner recorded on the pod.
func Owner(pod *corev1.Pod) string {
	if pod == nil {
		return ""
	}
	return pod.Annotations[annotationOwner]
}

func (m *Manager) Get(ctx context.Context, name string) (*corev1.Pod, error) {
	pod, err := m.Client.CoreV1().Pods(m.Namespace).Get(ctx, PodName(name), metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if pod.Labels[labelWorkspace] != name {
		return nil, fmt.Errorf("workspace %q not found", name)
	}
	return pod, nil
}

// GetWorkspace returns the workspace details (running or stopped).
func (m *Manager) GetWorkspace(ctx context.Context, name string) (Workspace, error) {
	pod, err := m.Get(ctx, name)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return Workspace{}, err
		}
		// Not running, check stopped state
		state, err := m.State(ctx, name)
		if err != nil {
			return Workspace{}, err
		}
		return workspaceFromState(state, "Stopped"), nil
	}
	return toWorkspace(pod), nil
}

type resolvedCreate struct {
	templateName string
	template     *templates.Template
	params       map[string]string
	storage      string
	pod          *corev1.Pod
}

func (m *Manager) resolveCreate(req CreateRequest) (resolvedCreate, error) {
	if m.Templates == nil {
		return resolvedCreate{}, fmt.Errorf("template catalog is not configured")
	}

	templateName := req.Template
	if templateName == "" {
		templateName = m.DefaultTemplate
	}
	if templateName == "" {
		templateName = "base"
	}
	tmpl, err := m.Templates.Get(templateName)
	if err != nil {
		return resolvedCreate{}, err
	}

	params, err := tmpl.Resolve(req.Parameters)
	if err != nil {
		return resolvedCreate{}, err
	}
	if req.CPU != "" {
		params["cpu"] = req.CPU
	}
	if req.Memory != "" {
		params["memory"] = req.Memory
	}
	if req.Storage != "" {
		params["storage"] = req.Storage
	}
	if _, ok := params["storageClass"]; ok && strings.TrimSpace(params["storageClass"]) == "" && m.DefaultStorageClass != "" {
		params["storageClass"] = m.DefaultStorageClass
	}
	storage := params["storage"]

	image := req.Image
	if image == "" {
		image = tmpl.Image
	}
	data := templates.Data{
		Name:           req.Name,
		Namespace:      m.Namespace,
		PodName:        PodName(req.Name),
		PVCName:        PodName(req.Name),
		Image:          image,
		Parameters:     params,
		Storage:        storage,
		AuthorizedKeys: strings.Join(m.authorizedKeysFor(req.Owner), "\n"),
	}
	pod, err := tmpl.Render(data)
	if err != nil {
		return resolvedCreate{}, err
	}
	rewritePodImages(pod, m.ImageRegistry)
	applyImagePullSecrets(pod, m.ImagePullSecrets)
	if !hasContainer(pod, ContainerName) {
		return resolvedCreate{}, fmt.Errorf("template %q must define a container named %q", templateName, ContainerName)
	}
	return resolvedCreate{
		templateName: templateName,
		template:     tmpl,
		params:       params,
		storage:      storage,
		pod:          pod,
	}, nil
}

// PreviewUsage renders a create request without touching Kubernetes and
// returns its resource requests.
func (m *Manager) PreviewUsage(req CreateRequest) (ResourceUsage, error) {
	resolved, err := m.resolveCreate(req)
	if err != nil {
		return ResourceUsage{}, err
	}
	return resourceUsage(resolved.pod.Spec, resolved.storage)
}

func (m *Manager) Create(ctx context.Context, req CreateRequest) (Workspace, error) {
	req.Name = strings.TrimSpace(req.Name)
	if err := validateName(req.Name); err != nil {
		return Workspace{}, err
	}
	release, err := m.lockWorkspace(ctx, req.Name)
	if err != nil {
		return Workspace{}, err
	}
	defer release()
	return m.create(ctx, req)
}

func (m *Manager) create(ctx context.Context, req CreateRequest) (Workspace, error) {
	req.Name = strings.TrimSpace(req.Name)
	if err := validateName(req.Name); err != nil {
		return Workspace{}, err
	}
	if req.Schedule != nil {
		schedule := *req.Schedule
		if err := schedule.Normalize(); err != nil {
			return Workspace{}, err
		}
		if schedule.Empty() {
			req.Schedule = nil
		} else {
			req.Schedule = cloneSchedule(&schedule)
		}
	}
	if _, err := m.GetWorkspace(ctx, req.Name); err == nil {
		return Workspace{}, fmt.Errorf("workspace %q already exists", req.Name)
	} else if !apierrors.IsNotFound(err) {
		return Workspace{}, err
	}

	resolved, err := m.resolveCreate(req)
	if err != nil {
		return Workspace{}, err
	}
	usage, err := resourceUsage(resolved.pod.Spec, resolved.storage)
	if err != nil {
		return Workspace{}, err
	}
	if req.QuotaCheck != nil {
		if err := req.QuotaCheck(usage); err != nil {
			return Workspace{}, err
		}
	}

	templateName := resolved.templateName
	tmpl := resolved.template
	params := resolved.params
	storage := resolved.storage
	createdPVC := false
	if storage != "" {
		createdPVC, err = m.createPVC(ctx, req.Name, templateName, storage, params["storageClass"])
		if err != nil {
			return Workspace{}, err
		}
	}

	pod := resolved.pod
	pod.Name = PodName(req.Name)
	pod.Namespace = m.Namespace
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	for key, value := range labels(req.Name) {
		pod.Labels[key] = value
	}
	pod.Labels[labelTemplate] = templateName
	if req.Owner != "" {
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[annotationOwner] = req.Owner
	}
	if err := m.applySSHKeys(ctx, pod, req.Name, req.Owner); err != nil {
		if createdPVC {
			_ = m.deletePVC(ctx, req.Name)
		}
		return Workspace{}, fmt.Errorf("SSH keys: %w", err)
	}
	if pod.Spec.RestartPolicy == "" {
		pod.Spec.RestartPolicy = corev1.RestartPolicyAlways
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	if req.ExternalAuthReady {
		applyExternalAuthEnv(pod, req.Owner, req.Name, req.PortalURL, tmpl.ExternalAuth, req.ExternalAuthEnv, req.WorkspaceToken)
	} else if err := m.applyExternalAuth(ctx, pod, req.Owner, req.Name, req.PortalURL, tmpl.ExternalAuth); err != nil {
		if createdPVC {
			_ = m.deletePVC(ctx, req.Name)
		}
		return Workspace{}, err
	}
	if err := m.applyAgentEnv(pod, req.Name, req.Owner); err != nil {
		if createdPVC {
			_ = m.deletePVC(ctx, req.Name)
		}
		return Workspace{}, fmt.Errorf("workspace agent: %w", err)
	}
	stateSpec := *pod.Spec.DeepCopy()
	stripExternalAuthEnv(&stateSpec, externalAuthEnvNames(pod))
	state := WorkspaceState{
		Name:            req.Name,
		Template:        templateName,
		TemplateVersion: tmpl.Version,
		Owner:           req.Owner,
		Image:           containerImage(pod, ContainerName),
		CreatedAt:       time.Now().UTC(),
		Persistent:      storage != "",
		Storage:         storage,
		StorageClass:    params["storageClass"],
		Parameters:      params,
		Schedule:        cloneSchedule(req.Schedule),
		PortalURL:       req.PortalURL,
		Labels:          copyStringMap(pod.Labels),
		Annotations:     copyStringMap(pod.Annotations),
		Spec:            stateSpec,
	}
	raw, err := marshalState(state)
	if err != nil {
		if createdPVC {
			_ = m.deletePVC(ctx, req.Name)
		}
		return Workspace{}, err
	}
	pod.Annotations[annotationState] = raw

	created, err := m.Client.CoreV1().Pods(m.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if createdPVC {
			_ = m.deletePVC(ctx, req.Name)
		}
		return Workspace{}, err
	}
	return toWorkspace(created), nil
}

func (m *Manager) Delete(ctx context.Context, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	release, err := m.lockWorkspace(ctx, name)
	if err != nil {
		return err
	}
	defer release()
	return m.delete(ctx, name)
}

func (m *Manager) delete(ctx context.Context, name string) error {
	pod, err := m.Get(ctx, name)
	podExists := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	states, err := m.stateStore().Load(ctx)
	if err != nil {
		return err
	}
	_, stateExists := states[name]
	if !podExists && !stateExists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "workspaces"}, name)
	}

	if podExists {
		grace := int64(0)
		err := m.Client.CoreV1().Pods(m.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
			GracePeriodSeconds: &grace,
		})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if err := m.deletePVC(ctx, name); err != nil {
		return err
	}
	if err := m.deleteSSHKeysSecret(ctx, name); err != nil {
		return err
	}
	if stateExists {
		if err := m.stateStore().Update(ctx, func(states map[string]WorkspaceState) error {
			delete(states, name)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// Stop deletes the workspace pod but keeps its persistent volume and a stored
// copy of the rendered spec so it can be started again.
func (m *Manager) authorizedKeysFor(owner string) []string {
	var keys []string
	if m.SSHKeys != nil {
		keys = m.SSHKeys.ForOwner(owner)
	} else {
		keys = m.AuthorizedKeys
	}
	if m.SSHCA == nil {
		return keys
	}
	ca := m.SSHCA.AuthorizedKeyUnrestricted()
	if m.SSHRequireAgent {
		ca = m.SSHCA.AuthorizedKey()
	}
	if ca == "" {
		return keys
	}
	out := make([]string, 0, len(keys)+1)
	out = append(out, keys...)
	out = append(out, ca)
	return out
}

func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if len(name) > 56 {
		return fmt.Errorf("name must be 56 characters or less")
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return fmt.Errorf("invalid name %q: %s", name, errs[0])
	}
	return nil
}
