package orasartifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/distribution/distribution/v3"
	"github.com/opencontainers/go-digest"
	v1 "github.com/oras-project/artifacts-spec/specs-go/v1"
)

const CreateAnnotationName = "io.cncf.oras.artifact.created"
const CreateAnnotationTimestampFormat = time.RFC3339

func init() {
	unmarshalFunc := func(b []byte) (distribution.Manifest, distribution.Descriptor, error) {
		d := new(DeserializedManifest)
		err := d.UnmarshalJSON(b)
		if err != nil {
			return nil, distribution.Descriptor{}, err
		}

		dgst := digest.FromBytes(b)
		return d, distribution.Descriptor{Digest: dgst, Size: int64(len(b)), MediaType: v1.MediaTypeArtifactManifest}, err
	}
	err := distribution.RegisterManifestSchema(v1.MediaTypeArtifactManifest, unmarshalFunc)
	if err != nil {
		panic(fmt.Sprintf("Unable to register ORAS artifact manifest: %s", err))
	}
}

// Manifest describes ORAS artifact manifests.
type Manifest struct {
	Inner v1.Manifest
}

// ArtifactType returns the artifactType of this ORAS artifact.
func (a Manifest) ArtifactType() string {
	return a.Inner.ArtifactType
}

// Annotations returns the annotations of this ORAS artifact.
func (a Manifest) Annotations() map[string]string {
	return a.Inner.Annotations
}

// MediaType returns the media type of this ORAS artifact.
func (a Manifest) MediaType() string {
	return a.Inner.MediaType
}

// References returns the distribution descriptors for the referenced blobs.
func (a Manifest) References() []distribution.Descriptor {
	blobs := make([]distribution.Descriptor, len(a.Inner.Blobs))
	for i := range a.Inner.Blobs {
		blobs[i] = distribution.Descriptor{
			MediaType: a.Inner.Blobs[i].MediaType,
			Digest:    a.Inner.Blobs[i].Digest,
			Size:      a.Inner.Blobs[i].Size,
		}
	}
	return blobs
}

// Subject returns the the subject manifest this artifact references.
func (a Manifest) Subject() distribution.Descriptor {
	return distribution.Descriptor{
		MediaType: a.Inner.Subject.MediaType,
		Digest:    a.Inner.Subject.Digest,
		Size:      a.Inner.Subject.Size,
	}
}

// DeserializedManifest wraps Manifest with a copy of the original JSON data.
type DeserializedManifest struct {
	Manifest

	// Raw is the Raw byte representation of the ORAS artifact.
	Raw []byte
}

// UnmarshalJSON populates a new Manifest struct from JSON data.
func (d *DeserializedManifest) UnmarshalJSON(b []byte) error {
	d.Raw = make([]byte, len(b))
	copy(d.Raw, b)

	var man v1.Manifest
	if err := json.Unmarshal(d.Raw, &man); err != nil {
		return err
	}
	if man.ArtifactType == "" {
		return errors.New("artifactType cannot be empty")
	}
	if man.MediaType != v1.MediaTypeArtifactManifest {
		return errors.New("mediaType is invalid")
	}

	d.Inner = man

	return nil
}

// MarshalJSON returns the raw content.
func (d *DeserializedManifest) MarshalJSON() ([]byte, error) {
	if len(d.Raw) > 0 {
		return d.Raw, nil
	}

	return nil, errors.New("JSON representation not initialized in DeserializedManifest")
}

// Payload returns the raw content of the Artifact. The contents can be
// used to calculate the content identifier.
func (d DeserializedManifest) Payload() (string, []byte, error) {
	// NOTE: This is a hack. The media type should be read from storage.
	return v1.MediaTypeArtifactManifest, d.Raw, nil
}
