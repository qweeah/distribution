package oci

import (
	"context"
	"net/http"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/configuration"
	v2 "github.com/distribution/distribution/v3/registry/api/v2"
	"github.com/distribution/distribution/v3/registry/extension"
	"github.com/distribution/distribution/v3/registry/storage"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/gorilla/handlers"
	"gopkg.in/yaml.v2"
)

const (
	namespaceName         = "oci"
	extensionName         = "ext"
	discoverComponentName = "discover"
	namespaceUrl          = "https://github.com/opencontainers/distribution-spec/blob/main/extensions/_oci.md"
	namespaceDescription  = "oci extension enables listing of supported registry and repository extensions"
)

type ociNamespace struct {
	storageDriver   driver.StorageDriver
	discoverEnabled bool
}

type ociOptions struct {
	RegExtensionComponents []string `yaml:"ext,omitempty"`
}

// newOciNamespace creates a new extension namespace with the name "oci"
func newOciNamespace(ctx context.Context, storageDriver driver.StorageDriver, options configuration.ExtensionConfig) (extension.Extension, error) {
	optionsYaml, err := yaml.Marshal(options)
	if err != nil {
		return nil, err
	}

	var ociOption ociOptions
	err = yaml.Unmarshal(optionsYaml, &ociOption)
	if err != nil {
		return nil, err
	}

	discoverEnabled := false
	for _, component := range ociOption.RegExtensionComponents {
		switch component {
		case "discover":
			discoverEnabled = true
		}
	}

	return &ociNamespace{
		storageDriver:   storageDriver,
		discoverEnabled: discoverEnabled,
	}, nil
}

func init() {
	// register the extension namespace.
	extension.RegisterExtension(namespaceName, newOciNamespace)
}

// GetManifestHandlers returns a list of manifest handlers that will be registered in the manifest store.
func (o *ociNamespace) GetManifestHandlers(repo distribution.Repository, blobStore distribution.BlobStore) []storage.ManifestHandler {
	// This extension doesn't extend any manifest store operations.
	return []storage.ManifestHandler{}
}

func (o *ociNamespace) GetGarbageCollectionHandlers() []storage.GCExtensionHandler {
	// This extension doesn't extend any garbage collection operations.
	return []storage.GCExtensionHandler{}
}

// GetRepositoryRoutes returns a list of extension routes scoped at a repository level
func (o *ociNamespace) GetRepositoryRoutes() []extension.ExtensionRoute {
	var routes []extension.ExtensionRoute

	if o.discoverEnabled {
		routes = append(routes, extension.ExtensionRoute{
			Namespace: namespaceName,
			Extension: extensionName,
			Component: discoverComponentName,
			Descriptor: v2.RouteDescriptor{
				Entity: "Extension",
				Methods: []v2.MethodDescriptor{
					{
						Method:      "GET",
						Description: "Get all extensions enabled for a repository.",
					},
				},
			},
			Dispatcher: o.discoverDispatcher,
		})
	}

	return routes
}

// GetRegistryRoutes returns a list of extension routes scoped at a registry level
func (o *ociNamespace) GetRegistryRoutes() []extension.ExtensionRoute {
	var routes []extension.ExtensionRoute

	if o.discoverEnabled {
		routes = append(routes, extension.ExtensionRoute{
			Namespace: namespaceName,
			Extension: extensionName,
			Component: discoverComponentName,
			Descriptor: v2.RouteDescriptor{
				Entity: "Extension",
				Methods: []v2.MethodDescriptor{
					{
						Method:      "GET",
						Description: "Get all extensions enabled for a registry.",
					},
				},
			},
			Dispatcher: o.discoverDispatcher,
		})
	}

	return routes
}

// GetNamespaceName returns the name associated with the namespace
func (o *ociNamespace) GetNamespaceName() string {
	return namespaceName
}

// GetNamespaceUrl returns the url link to the documentation where the namespace's extension and endpoints are defined
func (o *ociNamespace) GetNamespaceUrl() string {
	return namespaceUrl
}

// GetNamespaceDescription returns the description associated with the namespace
func (o *ociNamespace) GetNamespaceDescription() string {
	return namespaceDescription
}

func (o *ociNamespace) discoverDispatcher(ctx *extension.ExtensionContext, r *http.Request) http.Handler {
	extensionHandler := &extensionHandler{
		ExtensionContext: ctx,
		storageDriver:    o.storageDriver,
	}

	return handlers.MethodHandler{
		"GET": http.HandlerFunc(extensionHandler.getExtensions),
	}
}
