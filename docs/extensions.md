---
description: High level discussion of extensions
keywords: registry, extension, handlers, repository, distribution, artifacts
title: Extensions
---

This document serves as a high level discussion of the implementation of the extensions framework defined in the [OCI Distribution spec](https://github.com/opencontainers/distribution-spec/tree/main/extensions).

## Extension Interface

The `Extension` interface is introduced in the new `extension` package. It defines methods to access the extension's namespace-specific attributes such as the Name, Url defining the extension namespace, and the Description of the namespace. It defines route enumeration at the Registry and Repository level. It also encases the `ExtendedStorage` interface which defines the methods requires to extend the underlying storage functionality of the registry. 

```
type Extension interface {
	storage.ExtendedStorage
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
```

The `ExtendedStorage` interface defines methods that specify storage-specific handlers. Each extension will implement a handler extending the functionality. The interface can be expanded in the future to consider new handler types.
`GetManifestHandlers` is used to return new `ManifestHandlers` defined by each of the extensions.
`GetGarbageCollectionHandlers` is used to return `GCExtensionHandler` implemented by each extension.

```
type ExtendedStorage interface {
	// GetManifestHandlers returns the list of manifest handlers that handle custom manifest formats supported by the extension
	GetManifestHandlers(
		repo Repository,
		blobStore BlobStore) []ManifestHandler
    // GetGarbageCollectHandlers returns the GCExtensionHandlers that handles custom garbage collection behavior for the extension.
	GetGarbageCollectionHandlers() []GCExtensionHandler
}
```

The `GCExtensionHandler` interface defines three methods that are used in the garbage colection mark and sweep process. The `Mark` method is invoked for each `GCExtensionHandler` after the existing mark process finishes in `MarkAndSweep`. It is used to determine if the manifest and blobs should have their temporary ref count incremented in the case of an artifact manifest, or if the manifest and it's referrers should be recursively indexed for deletion in the case of a non-artifact manifest. `OnManifestDelete` is invoked to extend the `RemoveManifest` functionality for the `Vacuum`. New or special-cased manifests may require custom manifest deletion which can be defined with this method. `SweepBlobs` is used to add artifact manifest/blobs to the original `markSet`. These blobs are retained after determining their ref count is still positive. 

```
type GCExtensionHandler interface {
	Mark(ctx context.Context,
		repository distribution.Repository,
		storageDriver driver.StorageDriver,
		registry distribution.Namespace,
		manifest distribution.Manifest,
		manifestDigest digest.Digest,
		dryRun bool,
		removeUntagged bool) (bool, error)
	OnManifestDelete(ctx context.Context,
		storageDriver driver.StorageDriver,
		registry distribution.Namespace,
		dgst digest.Digest,
		repositoryName string) error
	SweepBlobs(ctx context.Context,
		markSet map[digest.Digest]struct{}) map[digest.Digest]struct{}
}
```

## Registering Extensions

Extensions are defined in the configuration yaml. 

### Sample Extension Configuration YAML
```
# Configuration for extensions. It follows the below schema
# extensions
#   namespace:
#     configuration for the extension and its components in any schema specific to that namespace
extensions:
  oci: 
    ext:
      - discover # enable the discovery extension
```

Each `Extension` defined must call the `RegisterExtension` method to register an extension initialization function with the extension namespace name. The registered extension list is then used during configuration parsing to get and initialize the specified extension. (`GetExtension`)

```
// InitExtension is the initialize function for creating the extension namespace
type InitExtension func(ctx context.Context, storageDriver driver.StorageDriver, options configuration.ExtensionConfig) (Extension, error)

// RegisterExtension is used to register an InitExtension for
// an extension with the given name.
func RegisterExtension(name string, initFunc InitExtension)

// GetExtension constructs an extension with the given options using the given name.
func GetExtension(ctx context.Context, name string, storageDriver driver.StorageDriver, options configuration.ExtensionConfig) (Extension, error)
```

Each `Extension` defines an `ExtensionRoute` which contains the new `<namespace>/<extension>/<component>` route attributes. Furthermore, the route `Descriptor` and `Dispatcher` are used to register the new route to the application. 

```
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

// RouteDispatchFunc is the http route dispatcher used by the extension route handlers
type RouteDispatchFunc func(extContext *ExtensionContext, r *http.Request) http.Handler
```



