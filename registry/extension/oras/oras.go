package oras

import (
	"context"
	"net/http"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/configuration"
	dcontext "github.com/distribution/distribution/v3/context"
	v2 "github.com/distribution/distribution/v3/registry/api/v2"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/gorilla/handlers"
	"github.com/opencontainers/go-digest"
	"gopkg.in/yaml.v2"
)

const (
	namespaceName          = "oras"
	extensionName          = "artifacts"
	referrersComponentName = "referrers"
	namespaceUrl           = "https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md"
	namespaceDescription   = "oras extension enables listing of all reference artifacts associated with subject"
)

type orasNamespace struct {
	storageDriver    driver.StorageDriver
	referrersEnabled bool
	gcHandler        orasGCHandler
}

type OrasOptions struct {
	ArtifactsExtComponents []string `yaml:"artifacts,omitempty"`
}

// newOrasNamespace creates a new extension namespace with the name "oras"
func newOrasNamespace(ctx context.Context, storageDriver driver.StorageDriver, options configuration.ExtensionConfig) (distribution.ExtendedNamespace, error) {
	optionsYaml, err := yaml.Marshal(options)
	if err != nil {
		return nil, err
	}

	var orasOptions OrasOptions
	err = yaml.Unmarshal(optionsYaml, &orasOptions)
	if err != nil {
		return nil, err
	}

	referrersEnabled := false
	for _, component := range orasOptions.ArtifactsExtComponents {
		if component == referrersComponentName {
			referrersEnabled = true
			break
		}
	}

	orasGCHandler := orasGCHandler{}

	return &orasNamespace{
		referrersEnabled: referrersEnabled,
		storageDriver:    storageDriver,
		gcHandler:        orasGCHandler,
	}, nil
}

func init() {
	distribution.RegisterExtension(namespaceName, newOrasNamespace)
}

// GetManifestHandlers returns a list of manifest handlers that will be registered in the manifest store.
func (o *orasNamespace) GetManifestHandlers(repo distribution.Repository, blobStore distribution.BlobStore) []distribution.ManifestHandler {
	if o.referrersEnabled {
		return []distribution.ManifestHandler{
			&artifactManifestHandler{
				repository:    repo,
				blobStore:     blobStore,
				storageDriver: o.storageDriver,
			}}
	}

	return []distribution.ManifestHandler{}
}

func (o *orasNamespace) GetGarbageCollectionHandlers() []distribution.GCExtensionHandler {
	if o.referrersEnabled {
		return []distribution.GCExtensionHandler{
			&o.gcHandler,
		}
	}

	return []distribution.GCExtensionHandler{}
}

// GetRepositoryRoutes returns a list of extension routes scoped at a repository level
func (d *orasNamespace) GetRepositoryRoutes() []distribution.ExtensionRoute {
	var routes []distribution.ExtensionRoute

	if d.referrersEnabled {
		routes = append(routes, distribution.ExtensionRoute{
			Namespace: namespaceName,
			Extension: extensionName,
			Component: referrersComponentName,
			Descriptor: v2.RouteDescriptor{
				Entity: "Referrers",
				Methods: []v2.MethodDescriptor{
					{
						Method:      "GET",
						Description: "Get all referrers for the given digest. Currently the API doesn't support pagination.",
					},
				},
			},
			Dispatcher: d.referrersDispatcher,
		})
	}

	return routes
}

// GetRegistryRoutes returns a list of extension routes scoped at a registry level
// There are no registry scoped routes exposed by this namespace
func (d *orasNamespace) GetRegistryRoutes() []distribution.ExtensionRoute {
	return nil
}

// GetNamespaceName returns the name associated with the namespace
func (d *orasNamespace) GetNamespaceName() string {
	return namespaceName
}

// GetNamespaceUrl returns the url link to the documentation where the namespace's extension and endpoints are defined
func (d *orasNamespace) GetNamespaceUrl() string {
	return namespaceUrl
}

// GetNamespaceDescription returns the description associated with the namespace
func (d *orasNamespace) GetNamespaceDescription() string {
	return namespaceDescription
}

func (o *orasNamespace) referrersDispatcher(extCtx *distribution.ExtensionContext, r *http.Request) http.Handler {

	handler := &referrersHandler{
		storageDriver: o.storageDriver,
		extContext:    extCtx,
	}
	q := r.URL.Query()
	if dgstStr := q.Get("digest"); dgstStr == "" {
		dcontext.GetLogger(extCtx).Errorf("digest not available")
	} else if d, err := digest.Parse(dgstStr); err != nil {
		dcontext.GetLogger(extCtx).Errorf("error parsing digest=%q: %v", dgstStr, err)
	} else {
		handler.Digest = d
	}

	mhandler := handlers.MethodHandler{
		"GET": http.HandlerFunc(handler.getReferrers),
	}

	return mhandler
}
