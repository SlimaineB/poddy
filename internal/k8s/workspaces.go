package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	klabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	"poddy/internal/sshca"
	"poddy/internal/sshkeys"
	"poddy/internal/templates"
)

const (
	AppName                = "poddy"
	ContainerName          = "workspace"
	AgentContainerName     = "poddy-agent"
	BrowserVSCodeContainer = "code-server"
	JupyterLabContainer    = "jupyterlab"
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
	// from external auth so tokens are stripped from stopped StatefulSet templates.
	annotationExternalAuthEnv = "poddy.io/external-auth-env"
	annotationEnvironmentEnv  = "poddy.io/environment-env"
	annotationApps            = "poddy.io/apps"

	envPortalURL       = "PODDY_PORTAL_URL"
	envWorkspaceName   = "PODDY_WORKSPACE_NAME"
	envWorkspaceToken  = "PODDY_WORKSPACE_TOKEN"
	envExternalAuthIDs = "PODDY_EXTERNAL_AUTH_IDS"
	envGitAskpass      = "GIT_ASKPASS"
	envGitPrompt       = "GIT_TERMINAL_PROMPT"
	envAgentURL        = "PODDY_AGENT_URL"
	envAgentToken      = "PODDY_AGENT_TOKEN"
	envAgentSidecar    = "PODDY_AGENT_SIDECAR"
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
	JupyterLab    bool      `json:"jupyterLab,omitempty"`
	Apps          []string  `json:"apps"`
	CPU           string    `json:"cpu,omitempty"`
	Memory        string    `json:"memory,omitempty"`
	Storage       string    `json:"storage,omitempty"`
	Region        string    `json:"region,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	Schedule      *Schedule `json:"schedule,omitempty"`
	// Access is filled by the portal for the requesting user. It carries
	// presence and owner-approval state; it is never stored on the pod.
	Access *WorkspaceAccess `json:"access,omitempty"`
	// StoppedBySchedule is internal; it prevents the scheduler from restarting
	// a workspace that a user stopped manually.
	StoppedBySchedule bool `json:"-"`
}

type CreateRequest struct {
	Name       string            `json:"name"`
	Template   string            `json:"template"`
	Parameters map[string]string `json:"parameters"`
	Apps       []string          `json:"apps"`

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
	ImageRegistryMap    map[string]string
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
	Environment         EnvironmentFunc
	AgentURL            string
	AgentImage          string
	AgentToken          WorkspaceTokenFunc

	stateOnce sync.Once
	state     *StateStore

	informerOnce    sync.Once
	informerFactory informers.SharedInformerFactory
	informersSynced atomic.Bool
	podLister       corelisters.PodLister
	claimLister     corelisters.PersistentVolumeClaimLister
	stsLister       appsv1listers.StatefulSetLister
}

func PodName(name string) string {
	return StatefulSetName(name) + "-0"
}

func labels(name string) map[string]string {
	return map[string]string{
		labelName:      AppName,
		labelManagedBy: AppName,
		labelWorkspace: name,
	}
}

// StartInformers starts a cached read model for pods, PVCs and state
// ConfigMaps. List keeps using the direct API until the caches sync, so a
// failure to watch (for example missing RBAC) degrades instead of blocking.
func (m *Manager) StartInformers(ctx context.Context) {
	if m.Client == nil || m.Namespace == "" {
		return
	}
	m.informerOnce.Do(func() {
		factory := informers.NewSharedInformerFactoryWithOptions(m.Client, 0, informers.WithNamespace(m.Namespace))
		m.podLister = factory.Core().V1().Pods().Lister()
		m.claimLister = factory.Core().V1().PersistentVolumeClaims().Lister()
		m.stsLister = factory.Apps().V1().StatefulSets().Lister()
		m.informerFactory = factory
		factory.Start(ctx.Done())
		go func() {
			factory.WaitForCacheSync(ctx.Done())
			if ctx.Err() == nil {
				m.informersSynced.Store(true)
			}
		}()
	})
}

func (m *Manager) List(ctx context.Context) ([]Workspace, error) {
	if m.informersSynced.Load() {
		return m.listCached()
	}
	return m.listAPI(ctx)
}

func (m *Manager) listCached() ([]Workspace, error) {
	selector := klabels.SelectorFromSet(klabels.Set{labelName: AppName})
	statefulSets, err := m.stsLister.StatefulSets(m.Namespace).List(selector)
	if err != nil {
		return nil, err
	}
	pods, err := m.podLister.Pods(m.Namespace).List(selector)
	if err != nil {
		return nil, err
	}
	claims, err := m.claimLister.PersistentVolumeClaims(m.Namespace).List(selector)
	if err != nil {
		return nil, err
	}
	return assembleWorkspaces(statefulSets, pods, claims), nil
}

func (m *Manager) listAPI(ctx context.Context) ([]Workspace, error) {
	statefulSets, err := m.Client.AppsV1().StatefulSets(m.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelName + "=" + AppName,
	})
	if err != nil {
		return nil, err
	}
	pods, err := m.Client.CoreV1().Pods(m.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelName + "=" + AppName,
	})
	if err != nil {
		return nil, err
	}
	claims, err := m.Client.CoreV1().PersistentVolumeClaims(m.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelName + "=" + AppName,
	})
	if err != nil {
		return nil, err
	}

	stsPointers := make([]*appsv1.StatefulSet, 0, len(statefulSets.Items))
	for i := range statefulSets.Items {
		stsPointers = append(stsPointers, &statefulSets.Items[i])
	}
	podPointers := make([]*corev1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		podPointers = append(podPointers, &pods.Items[i])
	}
	claimPointers := make([]*corev1.PersistentVolumeClaim, 0, len(claims.Items))
	for i := range claims.Items {
		claimPointers = append(claimPointers, &claims.Items[i])
	}
	return assembleWorkspaces(stsPointers, podPointers, claimPointers), nil
}

func assembleWorkspaces(statefulSets []*appsv1.StatefulSet, pods []*corev1.Pod, claims []*corev1.PersistentVolumeClaim) []Workspace {
	storage := map[string]string{}
	for _, claim := range claims {
		name := claim.Labels[labelWorkspace]
		if name == "" {
			name = strings.TrimPrefix(claim.Name, "poddy-")
		}
		if quantity, ok := claim.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
			storage[name] = quantity.String()
		}
	}

	podsByWorkspace := map[string]*corev1.Pod{}
	for _, pod := range pods {
		name := pod.Labels[labelWorkspace]
		if name == "" {
			continue
		}
		podsByWorkspace[name] = pod
	}

	out := make([]Workspace, 0, len(statefulSets)+len(pods))
	seen := make(map[string]bool, len(statefulSets))
	for _, sts := range statefulSets {
		name := workspaceName(sts)
		if name == "" {
			continue
		}
		pod := podsByWorkspace[name]
		ws := workspaceFromStatefulSet(sts)
		ws.Phase = phaseForStatefulSet(sts, pod)
		if ws.Storage == "" {
			ws.Storage = storage[name]
		}
		out = append(out, ws)
		seen[name] = true
	}

	// Legacy bare pods created before the StatefulSet migration remain visible
	// until they are stopped or deleted.
	for _, pod := range pods {
		name := pod.Labels[labelWorkspace]
		if name == "" || seen[name] {
			continue
		}
		ws := toWorkspace(pod)
		if ws.Storage == "" {
			ws.Storage = storage[name]
		}
		out = append(out, ws)
		seen[name] = true
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// State returns the stored spec of a stopped or running workspace.
func (m *Manager) State(ctx context.Context, name string) (WorkspaceState, error) {
	if sts, err := m.getStatefulSet(ctx, name); err == nil {
		return stateFromStatefulSet(sts), nil
	} else if !apierrors.IsNotFound(err) {
		return WorkspaceState{}, err
	}

	store := m.stateStore()
	state, ok, err := store.Get(ctx, name)
	if err != nil {
		return WorkspaceState{}, err
	}
	if !ok {
		return WorkspaceState{}, apierrors.NewNotFound(schema.GroupResource{Resource: "workspaces"}, name)
	}
	if state.Name == "" {
		state.Name = name
	}
	return state, nil
}

func (m *Manager) stateStore() *StateStore {
	m.stateOnce.Do(func() {
		name := m.StateConfigMap
		if name == "" {
			name = DefaultStateConfigMap
		}
		m.state = NewStateStore(m.Client, m.Namespace, name)
	})
	return m.state
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
	if apierrors.IsNotFound(err) && PodName(name) != name {
		// Legacy workspaces used the workspace name as the pod name.
		pod, err = m.Client.CoreV1().Pods(m.Namespace).Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		return nil, err
	}
	if pod.Labels[labelWorkspace] != name {
		return nil, fmt.Errorf("workspace %q not found", name)
	}
	return pod, nil
}

func (m *Manager) GetStatefulSet(ctx context.Context, name string) (*appsv1.StatefulSet, error) {
	return m.getStatefulSet(ctx, name)
}

// GetWorkspace returns the workspace details (running or stopped).
func (m *Manager) GetWorkspace(ctx context.Context, name string) (Workspace, error) {
	workspace, _, err := m.GetWorkspacePod(ctx, name)
	return workspace, err
}

// GetWorkspacePod returns the workspace details and the running pod, if any.
// It fetches the pod once so callers that also need pod metadata do not issue
// a second API call.
func (m *Manager) GetWorkspacePod(ctx context.Context, name string) (Workspace, *corev1.Pod, error) {
	if sts, err := m.getStatefulSet(ctx, name); err == nil {
		pod, podErr := m.Client.CoreV1().Pods(m.Namespace).Get(ctx, PodName(name), metav1.GetOptions{})
		if podErr != nil && !apierrors.IsNotFound(podErr) {
			return Workspace{}, nil, podErr
		}
		workspace := workspaceFromStatefulSet(sts)
		var runningPod *corev1.Pod
		if podErr == nil {
			runningPod = pod
		}
		workspace.Phase = phaseForStatefulSet(sts, runningPod)
		workspace.StoppedBySchedule = sts.Annotations[annotationStoppedBySchedule] == "true"
		return workspace, pod, nil
	} else if !apierrors.IsNotFound(err) {
		return Workspace{}, nil, err
	}

	pod, err := m.Get(ctx, name)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return Workspace{}, nil, err
		}
		// Not running and not a StatefulSet: check legacy stopped state.
		state, err := m.State(ctx, name)
		if err != nil {
			return Workspace{}, nil, err
		}
		return workspaceFromState(state, "Stopped"), nil, nil
	}
	return toWorkspace(pod), pod, nil
}

type resolvedCreate struct {
	templateName string
	template     *templates.Template
	params       map[string]string
	storage      string
	apps         []string
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
	apps := NormalizeApps(req.Apps)
	if req.Apps != nil {
		if templateHasParameter(tmpl, "browserVSCode") {
			params["browserVSCode"] = strconv.FormatBool(hasApp(apps, AppVSCodeWeb))
		}
		if templateHasParameter(tmpl, "jupyterLab") {
			params["jupyterLab"] = strconv.FormatBool(hasApp(apps, AppJupyterLab))
		}
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
	if strings.TrimSpace(params["storageClass"]) == "" && m.DefaultStorageClass != "" {
		if params == nil {
			params = map[string]string{}
		}
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
		PVCName:        PVCName(req.Name),
		Image:          image,
		Parameters:     params,
		Storage:        storage,
		AuthorizedKeys: strings.Join(m.authorizedKeysFor(req.Owner), "\n"),
	}
	pod, err := tmpl.Render(data)
	if err != nil {
		return resolvedCreate{}, err
	}
	rewritePodImages(pod, m.ImageRegistry, m.ImageRegistryMap)
	applyImagePullSecrets(pod, m.ImagePullSecrets)
	if !hasContainer(pod, ContainerName) {
		return resolvedCreate{}, fmt.Errorf("template %q must define a container named %q", templateName, ContainerName)
	}
	if m.AgentURL != "" && m.AgentToken != nil && m.AgentImage != "" {
		upsertAgentSidecar(pod, m.agentSidecarImage(), m.AgentURL, req.Name, "")
	}
	return resolvedCreate{
		templateName: templateName,
		template:     tmpl,
		params:       params,
		storage:      storage,
		apps:         apps,
		pod:          pod,
	}, nil
}

func templateHasParameter(tmpl *templates.Template, name string) bool {
	for _, parameter := range tmpl.Parameters {
		if parameter.Name == name {
			return true
		}
	}
	return false
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
	cleanup := func() {
		if createdPVC {
			_ = m.deletePVC(ctx, req.Name)
		}
		_ = m.deleteEnvironmentSecret(ctx, req.Name)
	}
	if err := m.applyEnvironment(ctx, pod, req.Owner, templateName, req.Name); err != nil {
		cleanup()
		return Workspace{}, err
	}
	if err := m.applySSHKeys(ctx, pod, req.Name, req.Owner); err != nil {
		cleanup()
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
		cleanup()
		return Workspace{}, err
	}
	if err := m.applyAgentEnv(pod, req.Name, req.Owner); err != nil {
		cleanup()
		return Workspace{}, fmt.Errorf("workspace agent: %w", err)
	}
	if resolved.apps != nil {
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[annotationApps] = strings.Join(resolved.apps, ",")
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	setAnnotation(pod.Annotations, annotationTemplateVersion, tmpl.Version)
	setAnnotation(pod.Annotations, annotationStorage, storage)
	setAnnotation(pod.Annotations, annotationStorageClass, params["storageClass"])
	setAnnotation(pod.Annotations, annotationPortalURL, req.PortalURL)
	if params != nil {
		if raw, err := json.Marshal(params); err == nil {
			pod.Annotations[annotationParameters] = string(raw)
		}
	}
	if raw, err := json.Marshal(cloneSchedule(req.Schedule)); err == nil && req.Schedule != nil {
		pod.Annotations[annotationSchedule] = string(raw)
	}

	if err := m.ensureGoverningService(ctx); err != nil {
		cleanup()
		return Workspace{}, fmt.Errorf("governing service: %w", err)
	}

	sts := m.buildStatefulSet(req.Name, pod, 1)
	created, err := m.Client.AppsV1().StatefulSets(m.Namespace).Create(ctx, sts, metav1.CreateOptions{})
	if err != nil {
		cleanup()
		return Workspace{}, err
	}
	return workspaceWithPhase(workspaceFromStatefulSet(created), "Pending"), nil
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
	sts, stsErr := m.getStatefulSet(ctx, name)
	stsExists := stsErr == nil
	if stsErr != nil && !apierrors.IsNotFound(stsErr) {
		return stsErr
	}

	pod, podErr := m.Get(ctx, name)
	podExists := podErr == nil
	if podErr != nil && !apierrors.IsNotFound(podErr) {
		return podErr
	}

	states, err := m.stateStore().Load(ctx)
	if err != nil {
		return err
	}
	_, stateExists := states[name]
	if !stsExists && !podExists && !stateExists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "workspaces"}, name)
	}

	if stsExists {
		if err := m.Client.AppsV1().StatefulSets(m.Namespace).Delete(ctx, sts.Name, metav1.DeleteOptions{
			GracePeriodSeconds: ptr.To(int64(0)),
		}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		grace := int64(0)
		if err := m.Client.CoreV1().Pods(m.Namespace).Delete(ctx, PodName(name), metav1.DeleteOptions{
			GracePeriodSeconds: &grace,
		}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		if err := m.waitForPodDeleted(ctx, PodName(name), 20*time.Second); err != nil {
			return err
		}
	} else if podExists {
		grace := int64(0)
		if err := m.Client.CoreV1().Pods(m.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{
			GracePeriodSeconds: &grace,
		}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if err := m.deletePVC(ctx, name); err != nil {
		return err
	}
	if err := m.deleteSSHKeysSecret(ctx, name); err != nil {
		return err
	}
	if err := m.deleteEnvironmentSecret(ctx, name); err != nil {
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
