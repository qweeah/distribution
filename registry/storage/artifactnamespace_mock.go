package storage

import (
	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/registry/storage/driver"
)

type MockNamespace struct {
	storageDriver    driver.StorageDriver
	referrersEnabled bool
}

// GetManifestHandlers returns a list of manifest handlers that will be registered in the manifest store.
func (o *MockNamespace) GetManifestHandlers(repo distribution.Repository, blobStore distribution.BlobStore) []ManifestHandler {
	if o.referrersEnabled {
		return []ManifestHandler{
			&ArtifactManifestHandler{
				Repository:    repo,
				BlobStore:     blobStore,
				StorageDriver: o.storageDriver,
			}}
	}

	return []ManifestHandler{}
}
