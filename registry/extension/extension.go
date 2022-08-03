package extension

import (
	"context"
	"fmt"
	"net/http"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/configuration"
	"github.com/distribution/distribution/v3/registry/api/errcode"
	v2 "github.com/distribution/distribution/v3/registry/api/v2"
	"github.com/distribution/distribution/v3/registry/storage"
	"github.com/distribution/distribution/v3/registry/storage/driver"
)

// ExtensionContext contains the request specific context for use in across handlers.
type ExtensionContext struct {
	context.Context

	// Registry is the base namespace that is used by all extension namespaces
	Registry distribution.Namespace
	// Repository is a reference to a named repository
	Repository distribution.Repository
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

// Extension is the interface that is used to define extensions to the distribution.
type Extension interface {
	storage.ExtendedStorage
	// ExtensionService
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

type ExtensionService interface {
}

// InitExtension is the initialize function for creating the extension namespace
type InitExtension func(ctx context.Context, storageDriver driver.StorageDriver, options configuration.ExtensionConfig) (Extension, error)

// EnumerateExtension specifies extension information at the namespace level
type EnumerateExtension struct {
	Name        string   `json:"name"`
	Url         string   `json:"url"`
	Description string   `json:"description,omitempty"`
	Endpoints   []string `json:"endpoints"`
}

var extensions map[string]InitExtension
var extensionsNamespaces map[string]Extension

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

// RegisterExtension is used to register an InitExtension for
// an extension with the given name.
func RegisterExtension(name string, initFunc InitExtension) {
	if extensions == nil {
		extensions = make(map[string]InitExtension)
	}

	if _, exists := extensions[name]; exists {
		panic(fmt.Sprintf("namespace name already registered: %s", name))
	}

	extensions[name] = initFunc
}

// GetExtension constructs an extension with the given options using the given name.
func GetExtension(ctx context.Context, name string, storageDriver driver.StorageDriver, options configuration.ExtensionConfig) (Extension, error) {
	if extensions != nil {
		if extensionsNamespaces == nil {
			extensionsNamespaces = make(map[string]Extension)
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
