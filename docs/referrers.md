[[__TOC__]]

# ORAS Artifacts Distribution

This document describes an experimental prototype that implements the
[ORAS Artifact Manifest](https://github.com/oras-project/artifacts-spec) spec.

## Usage - Push, Discover, Pull

The following steps illustrate how ORAS artifacts can be stored and retrieved from a registry.
The artifact in this example is a Notary V2 [signature](https://github.com/notaryproject/nv2/tree/prototype-2/docs/nv2).

### Prerequisites

- Local registry prototype instance
- [docker-generate](https://github.com/shizhMSFT/docker-generate)
- [nv2](https://github.com/notaryproject/nv2)
- `curl`
- `jq`

### Push an image to your registry

```shell
# Initialize local registry variables
regIp="127.0.0.1" && \
  regPort="5000" && \
  registry="$regIp:$regPort" && \
  repo="busybox" && \
  tag="latest" && \
  image="$repo:$tag" && \
  reference="$registry/$image"

# Pull an image from docker hub and push to local registry
docker pull $image && \
  docker tag $image $reference && \
  docker push $reference
```

### Generate image manifest and sign it

```shell
# Generate self-signed certificates
openssl req \
  -x509 \
  -sha256 \
  -nodes \
  -newkey rsa:2048 \
  -days 365 \
  -subj "/CN=$regIp/O=example inc/C=IN/ST=Haryana/L=Gurgaon" \
  -addext "subjectAltName=IP:$regIp" \
  -keyout example.key \
  -out example.crt

# Generate image manifest
manifestFile="manifest-to-sign.json" && \
  docker generate manifest $image > $manifestFile

# Sign manifest
signatureFile="manifest-signature.jwt" && \
  nv2 sign --method x509 \
    -k example.key \
    -c example.crt \
    -r $reference \
    -o $signatureFile \
    file:$manifestFile
```

### Obtain manifest and signature digests

```shell
manifestDigest="sha256:$(sha256sum $manifestFile | cut -d " " -f 1)" && \
  signatureDigest="sha256:$(sha256sum $signatureFile | cut -d " " -f 1)"
```

### Create an Artifact file referencing the manifest that was signed and its signature as blob

```shell
artifactFile="artifact.json" && \
  artifactMediaType="application/vnd.cncf.oras.artifact.manifest.v1+json" && \
  artifactType="application/vnd.cncf.notary.v2" && \
  signatureMediaType="application/vnd.cncf.notary.signature.v2+jwt" && \
  signatureFileSize=`wc -c < $signatureFile` && \
  manifestMediaType="$(cat $manifestFile | jq -r '.mediaType')" && \
  manifestFileSize=`wc -c < $manifestFile`

cat <<EOF > $artifactFile
{
  "mediaType": "$artifactMediaType",
  "artifactType": "$artifactType",
  "blobs": [
    {
      "mediaType": "$signatureMediaType",
      "digest": "$signatureDigest",
      "size": $signatureFileSize
    }
  ],
  "subjectManifest": {
      "mediaType": "$manifestMediaType",
      "digest": "$manifestDigest",
      "size": $manifestFileSize
  }
}
EOF
```

### Obtain artifact digest

```shell
artifactDigest="sha256:$(sha256sum $artifactFile | cut -d " " -f 1)"
```

### Push signature and artifact

```shell
# Initiate blob upload and obtain PUT location
blobPutLocation=`curl -I -X POST -s http://$registry/v2/$repo/blobs/uploads/ | grep "Location: " | sed -e "s/Location: //;s/$/\&digest=$signatureDigest/;s/\r//"`

# Push signature blob
curl -X PUT -H "Content-Type: application/octet-stream" --data-binary @"$signatureFile" $blobPutLocation

# Push artifact
curl -X PUT --data-binary @"$artifactFile" -H "Content-Type: $artifactMediaType" "http://$registry/v2/$repo/manifests/$artifactDigest"
```

### List referrers

```shell
# Retrieve referrers
curl -s "http://$registry/oras/artifacts/v1/$repo/manifests/$manifestDigest/referrers?artifactType=$artifactType" | jq
```

### Verify signature

```shell
# Retrieve signature
artifactDigest=`curl -s "http://$registry/oras/artifacts/v1/$repo/manifests/$manifestDigest/referrers?artifactType=$artifactType" | jq -r '.references[0].digest'` && \
  signatureDigest=`curl -s "http://$registry/oras/artifacts/v1/$repo/manifests/$artifactDigest" | jq -r '.blobs[0].digest'` && \
  retrievedSignatureFile="retrieved-signature.json" && \
  curl -s http://$registry/v2/$repo/blobs/$signatureDigest > $retrievedSignatureFile

# Verify signature
nv2 verify \
  -f $retrievedSignatureFile \
  -c example.crt \
  file:$manifestFile
```

## Implementation

To power the [/referrers](https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md) API, the
referrers of a manifest are indexed in the repository store. The following example illustrates the creation of this
index.

The `nginx:v1` image is already persisted:

- repository: `nginx`
- digest: `sha256:111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m`
- tag: `v1.0`

The repository store layout is represented as:

```bash
<root>
└── v2
    └── repositories
        └── nginx
            └── _manifests
                └── revisions
                    └── sha256
                        └── 111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m
                            └── link
```

Push a signature as blob and an ORAS Artifact that contains a blobs property referencing the signature, with the
following properties:

- digest: `sha256:222ibbf80b44ce6be8234e6ff90a1ac34acbeb826903b02cfa0da11c82cb222i`
- `subjectManifest` digest: `sha256:111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m`
- `artifactType`: `application/vnd.example.artifact`

On `PUT`, the artifact appears as a manifest revision. Additionally, an index entry is created under
the subject to facilitate a lookup to the referrer. The index path where the entry is added is
`/ref/<artifactType>`, as shown below.

```
<root>
└── v2
    └── repositories
        └── nginx
            └── _manifests
                └── revisions
                    └── sha256
                        ├── 111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m
                        │   ├── link
                        │   └── ref
                        │       └── digest(application/vnd.example.artifact)
                        │           └── sha256
                        │               └── 222ibbf80b44ce6be8234e6ff90a1ac34acbeb826903b02cfa0da11c82cb222i
                        │                   └── link
                        └── 222ibbf80b44ce6be8234e6ff90a1ac34acbeb826903b02cfa0da11c82cb222i
                            └── link
```

Push another ORAS artifact with the following properties:

- digest: `sha256:333ic0c33ebc4a74a0a554c86ac2b28ddf3454a5ad9cf90ea8cea9f9e75c333i`
- `subjectManifest` digest: `sha256:111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m`
- `artifactType`: `application/vnd.another.example.artifact`

This results in an addition to the index as shown below.

```
<root>
└── v2
    └── repositories
        └── nginx
            └── _manifests
                └── revisions
                    └── sha256
                        ├── 111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m
                        │   ├── link
                        │   └── ref
                        │       ├── digest(application/vnd.example.artifact)
                        │       │   └── sha256
                        │       │       └── 222ibbf80b44ce6be8234e6ff90a1ac34acbeb826903b02cfa0da11c82cb222i
                        │       │           └── link
                        │       └── digest(application/vnd.another.example.artifact)
                        │           └── sha256
                        │               └── 333ic0c33ebc4a74a0a554c86ac2b28ddf3454a5ad9cf90ea8cea9f9e75c333i
                        │                   └── link
                        ├── 222ibbf80b44ce6be8234e6ff90a1ac34acbeb826903b02cfa0da11c82cb222i
                        │   └── link
                        └── 333ic0c33ebc4a74a0a554c86ac2b28ddf3454a5ad9cf90ea8cea9f9e75c333i
                            └── link
```
