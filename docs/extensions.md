---
description: High level discussion of extensions
keywords: registry, extension, handlers, repository, distribution, artifacts
title: Extensions
---

This document serves as a high level discussion of the implementation of the extensions framework defined in the [OCI Distribution spec](https://github.com/opencontainers/distribution-spec/tree/main/extensions).

## Extension Interface

The `Extension` interface is introduced in the `distribution` package. It defines methods to access the extension's namespace specific attributes such as the Name, Url defining the extension namespace, and the Description of the namespace. It defines route enumeration at the Registry and Repository level. It also encases the `ExtendedStorage` interface which defines the methods requires to extend the underlying storage functionality of the registry. 

```
type Extension interface {
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
```

The `Namespace` interface in the `distrubtion` package is modified to return a list of `Extensions` registered to the `Namespace`

```
type Namespace interface {
	// Scope describes the names that can be used with this Namespace. The
	// global namespace will have a scope that matches all names. The scope
	// effectively provides an identity for the namespace.
	Scope() Scope

	// Repository should return a reference to the named repository. The
	// registry may or may not have the repository but should always return a
	// reference.
	Repository(ctx context.Context, name reference.Named) (Repository, error)

	// Repositories fills 'repos' with a lexicographically sorted catalog of repositories
	// up to the size of 'repos' and returns the value 'n' for the number of entries
	// which were filled.  'last' contains an offset in the catalog, and 'err' will be
	// set to io.EOF if there are no more entries to obtain.
	Repositories(ctx context.Context, repos []string, last string) (n int, err error)

	// Blobs returns a blob enumerator to access all blobs
	Blobs() BlobEnumerator

	// BlobStatter returns a BlobStatter to control
	BlobStatter() BlobStatter

    // Extensions returns a list of Extension registered to the Namespace
	Extensions() []Extension
}
```

The `ExtendedStorage` interface defines methods that specify storage-specific handlers. Each extension will implement a handler extending the functionality. The interface can be expanded in the future to consider new handler types.
`GetManifestHandlers` is used to return new `ManifestHandlers` defined by each of the extensions. (Note: To support this interface in the `distribution` package, the `ManifestHandlers` interface has been moved to the `distribution` package)
`GetGarbageCollectionHandlers` is used to return `GCExtensionHandler` implemented by each extension.

```
type ExtendedStorage interface {
	// GetManifestHandlers returns the list of manifest handlers that handle custom manifest formats supported by the extensions.
	GetManifestHandlers(
		repo Repository,
		blobStore BlobStore) []ManifestHandler
    // GetGarbageCollectHandlers returns the list of GC handlers that handle custom garbage collection behavior for the extensions
	GetGarbageCollectionHandlers() []GCExtensionHandler
}
```

The `GCExtensionHandler` interface defines three methods that are used in the garbage colection mark and sweep process. The `Mark` method is invoked for each `GCExtensionHandler` after the existing mark process finishes in `MarkAndSweep`. `IsEligibleForDeletion` is used to define if a specific manifest set for deletion in `MarkAndSweep` should be eligible for deletion. Extensions may choose to special case certain manifest types in manifest deletion. `RemoveManifestVacuum` is invoked to extend the `RemoveManifest` functionality for the `Vacuum`. New or special-cased manifests may require custom manifest deletion which can be defined with this method.

```
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



