package kube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	typedcore "k8s.io/client-go/kubernetes/typed/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type Query struct {
	Resource                  Resource
	Namespace, Labels, Fields string
	ResourceVersion           string
	Table                     TableMode
}
type VersionedLister interface {
	ListVersion(context.Context, Query) ([]unstructured.Unstructured, string, error)
}

const listPageSize = 1000

type Backend interface {
	Resources(context.Context) ([]Resource, error)
	List(context.Context, Query) ([]unstructured.Unstructured, error)
	Logs(context.Context, string, string, string, bool, ...LogOptions) (string, error)
	Close()
}
type ResourceGetter interface {
	Get(context.Context, Resource, string, string) (unstructured.Unstructured, error)
}
type Editable interface {
	ResourceGetter
	Patch(context.Context, Resource, string, string, []byte) error
}
type Context struct{ Name, Cluster, Namespace string }
type Loader struct{ raw *clientcmdapi.Config }

func Load(path string) (*Loader, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = path
	raw, err := rules.Load()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	if len(raw.Contexts) == 0 {
		return nil, fmt.Errorf("no Kubernetes contexts configured; configure kubeconfig or try kqt --demo")
	}
	return &Loader{raw}, nil
}
func (l *Loader) Current() string { return l.raw.CurrentContext }
func (l *Loader) Contexts() []Context {
	var result []Context
	for name, c := range l.raw.Contexts {
		ns := c.Namespace
		if ns == "" {
			ns = "default"
		}
		result = append(result, Context{name, c.Cluster, ns})
	}
	slices.SortFunc(result, func(a, b Context) int { return strings.Compare(a.Name, b.Name) })
	return result
}
func (l *Loader) Open(name string) (Backend, string, string, error) {
	if name == "" {
		return nil, "", "", fmt.Errorf("no current context; choose one with --context NAME")
	}
	if _, ok := l.raw.Contexts[name]; !ok {
		return nil, "", "", fmt.Errorf("unknown kubeconfig context %q", name)
	}
	config := clientcmd.NewNonInteractiveClientConfig(*l.raw.DeepCopy(), name, &clientcmd.ConfigOverrides{}, nil)
	cfg, err := config.ClientConfig()
	if err != nil {
		return nil, "", "", err
	}
	cfg.UserAgent = "kqt/0.1.0"
	cfg.Timeout = time.Minute
	cfg.QPS, cfg.Burst = 50, 100
	ns, _, err := config.Namespace()
	if err != nil {
		return nil, "", "", err
	}
	b, err := NewClient(cfg)
	return b, cfg.Host, ns, err
}

type Client struct {
	dynamic       dynamic.Interface
	core          typedcore.CoreV1Interface
	http          *http.Client
	config        *rest.Config
	streamCore    typedcore.CoreV1Interface
	streamDynamic dynamic.Interface
	metricsDenied sync.Map
}

func NewClient(cfg *rest.Config) (*Client, error) {
	h, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, err
	}
	d, err := dynamic.NewForConfigAndClient(cfg, h)
	if err != nil {
		return nil, err
	}
	c, err := typedcore.NewForConfigAndClient(cfg, h)
	if err != nil {
		return nil, err
	}
	streamConfig := rest.CopyConfig(cfg)
	streamConfig.Timeout = 0
	streamHTTP := *h
	streamHTTP.Timeout = 0
	streamCore, err := typedcore.NewForConfigAndClient(streamConfig, &streamHTTP)
	if err != nil {
		return nil, err
	}
	streamDynamic, err := dynamic.NewForConfigAndClient(streamConfig, &streamHTTP)
	if err != nil {
		return nil, err
	}
	return &Client{dynamic: d, core: c, http: h, config: rest.CopyConfig(cfg), streamCore: streamCore, streamDynamic: streamDynamic}, nil
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) warm(ctx context.Context) {
	request, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	c.core.RESTClient().Get().AbsPath("/version").Do(request)
}

type discoveryTransport struct {
	base http.RoundTripper
	ctx  context.Context
}

func (t discoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.base.RoundTrip(r.Clone(t.ctx))
}
func (c *Client) Resources(ctx context.Context) ([]Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Discovery's public API has no context parameter. Bind its transport to
	// this request so context switches also cancel parallel discovery calls.
	h := *c.http
	base := h.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	h.Transport = discoveryTransport{base, ctx}
	disc, err := discovery.NewDiscoveryClientForConfigAndClient(c.config, &h)
	if err != nil {
		return nil, err
	}
	lists, err := disc.ServerPreferredResources()
	var result []Resource
	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") || !slices.Contains([]string(r.Verbs), "list") {
				continue
			}
			result = append(result, Resource{gv.WithResource(r.Name), r.Kind, r.Namespaced, r.ShortNames})
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return Merge(result), err
}
func (c *Client) List(ctx context.Context, q Query) ([]unstructured.Unstructured, error) {
	result, _, err := c.ListVersion(ctx, q)
	return result, err
}
func (c *Client) ListVersion(ctx context.Context, q Query) ([]unstructured.Unstructured, string, error) {
	page := c.pager(q)
	var result []unstructured.Unstructured
	var version string
	consistent := true
	options := metav1.ListOptions{Limit: listPageSize, LabelSelector: q.Labels, FieldSelector: q.Fields}
	for {
		items, pageVersion, next, err := page(ctx, options)
		if err != nil {
			return nil, "", err
		}
		if options.Continue == "" {
			version = pageVersion
		} else if version != pageVersion {
			consistent = false
		}
		result = append(result, items...)
		options.Continue = next
		if options.Continue == "" {
			break
		}
	}
	if !consistent {
		version = ""
	}
	return result, version, nil
}

type listPage func(context.Context, metav1.ListOptions) ([]unstructured.Unstructured, string, string, error)

func (c *Client) pager(q Query) listPage {
	if q.Table != TableOff {
		decoder := &tableDecoder{resource: q.Resource, namespace: q.Namespace}
		return func(ctx context.Context, options metav1.ListOptions) ([]unstructured.Unstructured, string, string, error) {
			return c.tablePage(ctx, q, options, decoder)
		}
	}
	var client dynamic.ResourceInterface = c.dynamic.Resource(q.Resource.GVR)
	if q.Resource.Namespaced {
		client = c.dynamic.Resource(q.Resource.GVR).Namespace(q.Namespace)
	}
	return func(ctx context.Context, options metav1.ListOptions) ([]unstructured.Unstructured, string, string, error) {
		list, err := client.List(ctx, options)
		if err != nil {
			return nil, "", "", err
		}
		for i := range list.Items {
			if list.Items[i].GetKind() == "" {
				list.Items[i].SetKind(q.Resource.Kind)
			}
			if list.Items[i].GetAPIVersion() == "" {
				list.Items[i].SetAPIVersion(q.Resource.GVR.GroupVersion().String())
			}
		}
		return list.Items, list.GetResourceVersion(), list.GetContinue(), nil
	}
}
func (c *Client) Logs(ctx context.Context, namespace, pod, container string, previous bool, limits ...LogOptions) (string, error) {
	limit := int64(1024 * 1024)
	options := logRequest(500, limits)
	options.Container, options.Previous, options.LimitBytes = container, previous, &limit
	stream, err := c.core.Pods(namespace).GetLogs(pod, options).Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(stream, limit))
	return string(data), err
}

func (c *Client) resource(r Resource, namespace string) dynamic.ResourceInterface {
	if r.Namespaced {
		return c.dynamic.Resource(r.GVR).Namespace(namespace)
	}
	return c.dynamic.Resource(r.GVR)
}
func (c *Client) Get(ctx context.Context, r Resource, namespace, name string) (unstructured.Unstructured, error) {
	o, err := c.resource(r, namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return unstructured.Unstructured{}, err
	}
	if o.GetKind() == "" {
		o.SetKind(r.Kind)
	}
	if o.GetAPIVersion() == "" {
		o.SetAPIVersion(r.GVR.GroupVersion().String())
	}
	return *o, nil
}
func (c *Client) Patch(ctx context.Context, r Resource, namespace, name string, patch []byte) error {
	_, err := c.resource(r, namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{FieldManager: "kqt-editor"})
	return err
}
