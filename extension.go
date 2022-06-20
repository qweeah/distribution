package distribution

import (
	"context"
	"fmt"
	"net/http"

	"github.com/distribution/distribution/v3/configuration"
	"github.com/distribution/distribution/v3/registry/api/errcode"
	v2 "github.com/distribution/distribution/v3/registry/api/v2"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/opencontainers/go-digest"
)

type Extension interface {
	ExtendedNamespace
}

// ExtensionContext contains the request specific context for use in across handlers.
type ExtensionContext struct {
	context.Context

	// Registry is the base namespace that is used by all extension namespaces
	Registry Namespace
	// Repository is a reference to a named repository
	Repository Repository
	// Errors are the set of errors that occurred within this request context
	Errors errcode.Errors
}

// RouteDispatchFunc is the http route dispatcher used by the extension route handlers
type RouteDispatchFunc func(extContext *ExtensionContext, r *http.Request) http.Handler

// ExtensionRoute describes an extension route.
type ExtensionRoute struct {
	// Namespace is the name of the extension namespace
	Namespace string
	// Extension is the name of the extension under the namespace
	Extension string
	// Component is the name of the component under the extension
	Component string
	// Descriptor is the route descriptor that gives its path
	Descriptor v2.RouteDescriptor
	// Dispatcher if present signifies that the route is http route with a dispatcher
	Dispatcher RouteDispatchFunc
}

type GCExtensionHandler interface {
	Mark(ctx context.Context,
		storageDriver driver.StorageDriver,
		registry Namespace,
		dryRun bool,
		removeUntagged bool) (map[digest.Digest]struct{}, error)
	RemoveManifestVacuum(ctx context.Context,
		storageDriver driver.StorageDriver,
		dgst digest.Digest,
		repositoryName string) error
	IsEligibleForDeletion(ctx context.Context,
		dgst digest.Digest,
		manifestService ManifestService) (bool, error)
}

// ExtendedStorage defines extensions to store operations like manifest for example.
type ExtendedStorage interface {
	// GetManifestHandlers returns the list of manifest handlers that handle custom manifest formats supported by the extensions.
	GetManifestHandlers(
		repo Repository,
		blobStore BlobStore) []ManifestHandler
	GetGarbageCollectionHandlers() []GCExtensionHandler
}

//Namespace is the namespace that is used to define extensions to the distribution.
type ExtendedNamespace interface {
	ExtendedStorage
	// GetRepositoryRoutes returns a list of extension routes scoped at a repository level
	GetRepositoryRoutes() []ExtensionRoute
	// GetRegistryRoutes returns a list of extension routes scoped at a registry level
	GetRegistryRoutes() []ExtensionRoute
	// GetNamespaceName returns the name associated with the namespace
	GetNamespaceName() string
	// GetNamespaceUrl returns the url link to the documentation where the namespace's extension and endpoints are defined
	GetNamespaceUrl() string
	// GetNamespaceDescription returns the description associated with the namespace
	GetNamespaceDescription() string
}

// // InitExtensionNamespace is the initialize function for creating the extension namespace
type InitExtensionNamespace func(ctx context.Context, storageDriver driver.StorageDriver, options configuration.ExtensionConfig) (ExtendedNamespace, error)

// EnumerateExtension specifies extension information at the namespace level
type EnumerateExtension struct {
	Name        string   `json:"name"`
	Url         string   `json:"url"`
	Description string   `json:"description,omitempty"`
	Endpoints   []string `json:"endpoints"`
}

var extensions map[string]InitExtensionNamespace
var extensionsNamespaces map[string]ExtendedNamespace

func EnumerateRegistered(ctx ExtensionContext) (enumeratedExtensions []EnumerateExtension) {
	for _, namespace := range extensionsNamespaces {
		enumerateExtension := EnumerateExtension{
			Name:        namespace.GetNamespaceName(),
			Url:         namespace.GetNamespaceUrl(),
			Description: namespace.GetNamespaceDescription(),
			Endpoints:   []string{},
		}

		scopedRoutes := namespace.GetRepositoryRoutes()

		// if the repository is not set in the context, scope is registry wide
		if ctx.Repository == nil {
			scopedRoutes = namespace.GetRegistryRoutes()
		}

		for _, route := range scopedRoutes {
			path := fmt.Sprintf("_%s/%s/%s", route.Namespace, route.Extension, route.Component)
			enumerateExtension.Endpoints = append(enumerateExtension.Endpoints, path)
		}

		// add extension to list if endpoints exist
		if len(enumerateExtension.Endpoints) > 0 {
			enumeratedExtensions = append(enumeratedExtensions, enumerateExtension)
		}
	}

	return enumeratedExtensions
}

// RegisterExtension is used to register an InitExtensionNamespace for
// an extension namespace with the given name.
func RegisterExtension(name string, initFunc InitExtensionNamespace) {
	if extensions == nil {
		extensions = make(map[string]InitExtensionNamespace)
	}

	if _, exists := extensions[name]; exists {
		panic(fmt.Sprintf("namespace name already registered: %s", name))
	}

	extensions[name] = initFunc
}

// GetExtension constructs an extension namespace with the given options using the given name.
func GetExtension(ctx context.Context, name string, storageDriver driver.StorageDriver, options configuration.ExtensionConfig) (ExtendedNamespace, error) {
	if extensions != nil {
		if extensionsNamespaces == nil {
			extensionsNamespaces = make(map[string]ExtendedNamespace)
		}

		if initFunc, exists := extensions[name]; exists {
			namespace, err := initFunc(ctx, storageDriver, options)
			if err == nil {
				// adds the initialized namespace to map for simple access to namespaces by EnumerateRegistered
				extensionsNamespaces[name] = namespace
			}
			return namespace, err
		}
	}

	return nil, fmt.Errorf("no extension registered with name: %s", name)
}
