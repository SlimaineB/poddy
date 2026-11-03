package templates

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"

	corev1 "k8s.io/api/core/v1"

	"poddy/internal/links"
	"sigs.k8s.io/yaml"
)

const (
	WorkspacePath = "/workspace"
	SSHCommand    = "/usr/local/bin/poddy-ssh-start"
)

//go:embed files
var embedded embed.FS

var (
	parameterName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*$`)
	envName       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Parameter struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Default     string `json:"default" yaml:"default"`
	Required    bool   `json:"required" yaml:"required"`
}

type ExternalAuth struct {
	ID       string   `json:"id" yaml:"id"`
	Optional bool     `json:"optional,omitempty" yaml:"optional,omitempty"`
	Env      []string `json:"env,omitempty" yaml:"env,omitempty"`
}

type Template struct {
	Name         string         `json:"name" yaml:"name"`
	DisplayName  string         `json:"displayName" yaml:"displayName"`
	Description  string         `json:"description" yaml:"description"`
	Image        string         `json:"image" yaml:"image"`
	Version      string         `json:"version" yaml:"version"`
	Parameters   []Parameter    `json:"parameters" yaml:"parameters"`
	Links        []links.Link   `json:"links,omitempty" yaml:"links,omitempty"`
	ExternalAuth []ExternalAuth `json:"externalAuth,omitempty" yaml:"externalAuth,omitempty"`
	Pod          string         `json:"-" yaml:"-"`
}

type Info struct {
	Name         string         `json:"name"`
	DisplayName  string         `json:"displayName"`
	Description  string         `json:"description"`
	Image        string         `json:"image"`
	Version      string         `json:"version"`
	Parameters   []Parameter    `json:"parameters"`
	Links        []links.Link   `json:"links,omitempty"`
	ExternalAuth []ExternalAuth `json:"externalAuth,omitempty"`
}

type Catalog struct {
	mu        sync.RWMutex
	templates map[string]*Template
	dir       string
}

// Source is a template loaded from an external store, such as a Kubernetes
// ConfigMap. Name is used when template.yaml does not set a name.
type Source struct {
	Name     string
	Version  string
	Template []byte
	Pod      []byte
}

type Data struct {
	Name           string
	Namespace      string
	PodName        string
	PVCName        string
	Image          string
	Parameters     map[string]string
	Storage        string
	AuthorizedKeys string
	SSHCommand     string
	WorkspacePath  string
}

func (d Data) Param(name string) string {
	return d.Parameters[name]
}

func Load(dir string) (*Catalog, error) {
	return loadSources(dir, nil)
}

// LoadSources builds a catalog from the embedded templates plus externally
// pushed sources. It is used by the Kubernetes ConfigMap watcher.
func LoadSources(sources []Source) (*Catalog, error) {
	return loadSources("", sources)
}

func loadSources(dir string, sources []Source) (*Catalog, error) {
	c := &Catalog{templates: map[string]*Template{}, dir: dir}
	if err := c.loadFS(embedded, "files"); err != nil {
		return nil, err
	}
	if dir != "" {
		if err := c.loadFS(os.DirFS(dir), "."); err != nil {
			return nil, err
		}
	}
	for _, source := range sources {
		if err := c.addTemplate(source.Name, source.Template, source.Pod, source.Version); err != nil {
			return nil, err
		}
	}
	if len(c.templates) == 0 {
		return nil, fmt.Errorf("no workspace templates found")
	}
	return c, nil
}

func (c *Catalog) loadFS(fsys fs.FS, root string) error {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if err := c.loadTemplate(fsys, name, path.Join(root, name)); err != nil {
				return err
			}
			continue
		}
		if !strings.HasSuffix(name, ".template.yaml") {
			continue
		}
		templateName := strings.TrimSuffix(name, ".template.yaml")
		if templateName == "" {
			continue
		}
		if err := c.loadFlatTemplate(fsys, root, templateName); err != nil {
			return err
		}
	}
	return nil
}

func (c *Catalog) loadTemplate(fsys fs.FS, name, dir string) error {
	data, err := fs.ReadFile(fsys, path.Join(dir, "template.yaml"))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	pod, err := fs.ReadFile(fsys, path.Join(dir, "pod.yaml"))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return c.addTemplate(name, data, pod, "")
}

func (c *Catalog) loadFlatTemplate(fsys fs.FS, root, name string) error {
	data, err := fs.ReadFile(fsys, path.Join(root, name+".template.yaml"))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	pod, err := fs.ReadFile(fsys, path.Join(root, name+".pod.yaml"))
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return c.addTemplate(name, data, pod, "")
}

func (c *Catalog) addTemplate(name string, data, pod []byte, version string) error {
	var t Template
	if err := yaml.Unmarshal(data, &t); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if t.Name == "" {
		t.Name = name
	}
	if t.Name != name {
		return fmt.Errorf("%s: template name %q does not match key", name, t.Name)
	}
	t.Pod = string(pod)
	if version != "" {
		t.Version = version
	}
	if t.DisplayName == "" {
		t.DisplayName = t.Name
	}
	t.Links = links.Normalize(t.Links)
	if err := t.validate(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	c.templates[t.Name] = &t
	return nil
}

// Reload replaces the catalog with a freshly loaded copy. If loading fails, the
// current catalog stays active. Templates already returned by Get remain valid.
func (c *Catalog) Reload(dir string) error {
	next, err := Load(dir)
	if err != nil {
		return err
	}
	c.swap(next)
	return nil
}

// ReloadSources replaces the catalog with embedded templates plus sources. If
// loading fails, the current catalog stays active.
func (c *Catalog) ReloadSources(sources []Source) error {
	c.mu.RLock()
	dir := c.dir
	c.mu.RUnlock()
	next, err := loadSources(dir, sources)
	if err != nil {
		return err
	}
	c.swap(next)
	return nil
}

func (c *Catalog) swap(next *Catalog) {
	c.mu.Lock()
	c.templates = next.templates
	c.dir = next.dir
	c.mu.Unlock()
}

func (t *Template) validate() error {
	if t.Image == "" {
		return fmt.Errorf("image is required")
	}
	if strings.TrimSpace(t.Pod) == "" {
		return fmt.Errorf("pod template is empty")
	}
	if err := links.Validate(t.Links); err != nil {
		return err
	}

	seenAuth := map[string]bool{}
	for _, auth := range t.ExternalAuth {
		if auth.ID == "" {
			return fmt.Errorf("external auth id is required")
		}
		if seenAuth[auth.ID] {
			return fmt.Errorf("duplicate external auth %q", auth.ID)
		}
		seenAuth[auth.ID] = true
		for _, env := range auth.Env {
			if !envName.MatchString(env) {
				return fmt.Errorf("invalid external auth env %q", env)
			}
		}
	}

	seen := map[string]bool{}
	for _, p := range t.Parameters {
		if !parameterName.MatchString(p.Name) {
			return fmt.Errorf("invalid parameter name %q", p.Name)
		}
		if p.Name == "name" || p.Name == "template" {
			return fmt.Errorf("parameter name %q is reserved", p.Name)
		}
		if seen[p.Name] {
			return fmt.Errorf("duplicate parameter %q", p.Name)
		}
		seen[p.Name] = true
	}
	return nil
}

func (c *Catalog) List() []Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.templates))
	for name := range c.templates {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Info, 0, len(names))
	for _, name := range names {
		t := c.templates[name]
		out = append(out, Info{
			Name:         t.Name,
			DisplayName:  t.DisplayName,
			Description:  t.Description,
			Image:        t.Image,
			Version:      t.Version,
			Parameters:   t.Parameters,
			Links:        t.Links,
			ExternalAuth: t.ExternalAuth,
		})
	}
	return out
}

func (c *Catalog) Get(name string) (*Template, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.templates[name]
	if !ok {
		return nil, fmt.Errorf("unknown template %q", name)
	}
	return t, nil
}

func (t *Template) Resolve(input map[string]string) (map[string]string, error) {
	params := make(map[string]string, len(t.Parameters))
	for _, p := range t.Parameters {
		params[p.Name] = p.Default
	}
	for name, value := range input {
		if _, ok := params[name]; !ok {
			return nil, fmt.Errorf("unknown parameter %q", name)
		}
		params[name] = value
	}
	for _, p := range t.Parameters {
		if p.Required && strings.TrimSpace(params[p.Name]) == "" {
			return nil, fmt.Errorf("parameter %q is required", p.Name)
		}
	}
	return params, nil
}

func (t *Template) Render(data Data) (*corev1.Pod, error) {
	if data.Image == "" {
		data.Image = t.Image
	}
	if data.WorkspacePath == "" {
		data.WorkspacePath = WorkspacePath
	}
	if data.Storage == "" {
		data.Storage = data.Parameters["storage"]
	}
	if data.SSHCommand == "" {
		data.SSHCommand = SSHCommand
	}
	funcs := template.FuncMap{"quote": strconv.Quote}
	tmpl, err := template.New("pod").Funcs(funcs).Parse(t.Pod)
	if err != nil {
		return nil, fmt.Errorf("parse pod template: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render pod template: %w", err)
	}
	var pod corev1.Pod
	if err := yaml.Unmarshal(buf.Bytes(), &pod); err != nil {
		return nil, fmt.Errorf("decode pod template: %w", err)
	}
	return &pod, nil
}
